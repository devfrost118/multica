// Package cursor reads an existing Cursor IDE WorkOS session from the local
// state database in read-only mode and queries personal usage. It deliberately
// contains no login, refresh, logout, OAuth, or session-persistence path.
package cursor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
	_ "modernc.org/sqlite"
)

const (
	cursorOrigin     = "https://cursor.com"
	defaultFreshness = 15 * time.Minute
)

type Config struct {
	StateDBPath string
	Client      *http.Client
	Now         func() time.Time
}

type Adapter struct {
	stateDBPath string
	client      *http.Client
	now         func() time.Time

	mu       sync.Mutex
	lastGood *providerlimits.AccountSnapshot
}

func NewAdapter(config Config) *Adapter {
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	path := strings.TrimSpace(config.StateDBPath)
	if path == "" {
		path = defaultStateDBPath()
	}
	return &Adapter{stateDBPath: path, client: client, now: now}
}

func (*Adapter) Provider() string { return "cursor" }

func (*Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 10 * time.Second, MinimumInterval: defaultFreshness}
}

func (a *Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	checkedAt := a.now().UTC()
	token, err := readAccessToken(a.stateDBPath)
	if err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "reauth_required")}, nil
	}
	claims, err := parseClaims(token)
	if err != nil || claims.Sub == "" || (claims.ExpiresAt > 0 && time.Unix(claims.ExpiresAt, 0).Before(checkedAt)) {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "reauth_required")}, nil
	}
	subject := bareSubject(claims.Sub)
	if subject == "" {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "reauth_required")}, nil
	}
	cookie := "WorkosCursorSessionToken=" + url.QueryEscape(subject+"::"+token)

	email, authStatus, err := a.fetchIdentity(ctx, cookie)
	if err != nil {
		return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errors.New("cursor identity unavailable")
	}
	if authStatus == http.StatusUnauthorized || authStatus == http.StatusForbidden {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "reauth_required")}, nil
	}
	if authStatus < http.StatusOK || authStatus >= http.StatusMultipleChoices {
		return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errors.New("cursor identity unavailable")
	}

	usage, status, err := a.fetchCurrentUsage(ctx, cookie)
	if err != nil {
		return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errors.New("cursor usage unavailable")
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "reauth_required")}, nil
	}
	snapshot := snapshotFromUsage(usage, subject, email, checkedAt)
	if status < http.StatusOK || status >= http.StatusMultipleChoices || len(snapshot.Buckets) == 0 {
		fallback, fallbackStatus, fallbackErr := a.fetchUsageSummary(ctx, cookie)
		if fallbackErr != nil || fallbackStatus < http.StatusOK || fallbackStatus >= http.StatusMultipleChoices {
			return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errors.New("cursor usage unavailable")
		}
		snapshot = snapshotFromUsage(fallback, subject, email, checkedAt)
	}
	if len(snapshot.Buckets) == 0 {
		return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errors.New("cursor usage unavailable")
	}
	a.storeLastGood(snapshot)
	return []providerlimits.AccountSnapshot{snapshot}, nil
}

type jwtClaims struct {
	Sub       string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
}

type identityResponse struct {
	Email string `json:"email"`
}

type usageResponse struct {
	BillingCycleStart json.RawMessage `json:"billingCycleStart"`
	BillingCycleEnd   json.RawMessage `json:"billingCycleEnd"`
	PlanUsage         planUsage       `json:"planUsage"`
	AutoPercentUsed   flexibleNumber  `json:"autoPercentUsed"`
	APIPercentUsed    flexibleNumber  `json:"apiPercentUsed"`
	TotalPercentUsed  flexibleNumber  `json:"totalPercentUsed"`
	TotalSpend        flexibleNumber  `json:"totalSpend"`
	IncludedSpend     flexibleNumber  `json:"includedSpend"`
	BonusSpend        flexibleNumber  `json:"bonusSpend"`
	Limit             flexibleNumber  `json:"limit"`
}

type planUsage struct {
	AutoPercentUsed  flexibleNumber `json:"autoPercentUsed"`
	APIPercentUsed   flexibleNumber `json:"apiPercentUsed"`
	TotalPercentUsed flexibleNumber `json:"totalPercentUsed"`
	TotalSpend       flexibleNumber `json:"totalSpend"`
	IncludedSpend    flexibleNumber `json:"includedSpend"`
	BonusSpend       flexibleNumber `json:"bonusSpend"`
	Limit            flexibleNumber `json:"limit"`
}

type flexibleNumber struct {
	Value float64
	Valid bool
}

func (number *flexibleNumber) UnmarshalJSON(raw []byte) error {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		*number = flexibleNumber{}
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		*number = flexibleNumber{}
		return nil
	}
	*number = flexibleNumber{Value: parsed, Valid: true}
	return nil
}

func readAccessToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("cursor state database unavailable")
	}
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", errors.New("cursor state database unavailable")
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA query_only = ON"); err != nil {
		return "", errors.New("cursor state database unavailable")
	}
	var token string
	if err := database.QueryRow(`SELECT value FROM ItemTable WHERE key='cursorAuth/accessToken'`).Scan(&token); err != nil {
		return "", errors.New("cursor session unavailable")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("cursor session unavailable")
	}
	return token, nil
}

func parseClaims(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("invalid cursor session")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, errors.New("invalid cursor session")
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, errors.New("invalid cursor session")
	}
	return claims, nil
}

func bareSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if index := strings.LastIndex(subject, "|"); index >= 0 {
		subject = subject[index+1:]
	}
	return strings.TrimSpace(subject)
}

func (a *Adapter) fetchIdentity(ctx context.Context, cookie string) (string, int, error) {
	request, err := newCursorRequest(ctx, http.MethodGet, "/api/auth/me", cookie, nil)
	if err != nil {
		return "", 0, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	var identity identityResponse
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity); err != nil {
			return "", response.StatusCode, err
		}
	}
	return strings.TrimSpace(identity.Email), response.StatusCode, nil
}

func (a *Adapter) fetchCurrentUsage(ctx context.Context, cookie string) (usageResponse, int, error) {
	request, err := newCursorRequest(ctx, http.MethodPost, "/api/dashboard/get-current-period-usage", cookie, []byte(`{}`))
	if err != nil {
		return usageResponse{}, 0, err
	}
	return a.doUsageRequest(request)
}

func (a *Adapter) fetchUsageSummary(ctx context.Context, cookie string) (usageResponse, int, error) {
	request, err := newCursorRequest(ctx, http.MethodGet, "/api/usage-summary", cookie, nil)
	if err != nil {
		return usageResponse{}, 0, err
	}
	return a.doUsageRequest(request)
}

func (a *Adapter) doUsageRequest(request *http.Request) (usageResponse, int, error) {
	response, err := a.client.Do(request)
	if err != nil {
		return usageResponse{}, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return usageResponse{}, response.StatusCode, nil
	}
	var usage usageResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&usage); err != nil {
		return usageResponse{}, response.StatusCode, err
	}
	return usage, response.StatusCode, nil
}

func newCursorRequest(ctx context.Context, method, path, cookie string, body []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, cursorOrigin+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Origin", cursorOrigin)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func snapshotFromUsage(response usageResponse, subject, email string, checkedAt time.Time) providerlimits.AccountSnapshot {
	usage := mergePlanUsage(response)
	resetsAt := timeFromJSON(response.BillingCycleEnd)
	snapshot := providerlimits.AccountSnapshot{
		Provider:     "cursor",
		AccountKey:   accountKeyFrom(subject, email),
		AccountLabel: maskEmail(email),
		CheckedAt:    checkedAt,
		Status:       providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalAuthState,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		Buckets: make([]providerlimits.Bucket, 0, 4),
	}
	snapshot.Buckets = appendPercentBucket(snapshot.Buckets, "auto", "Composer / Auto", usage.AutoPercentUsed, resetsAt)
	snapshot.Buckets = appendPercentBucket(snapshot.Buckets, "api", "Named / API models", usage.APIPercentUsed, resetsAt)
	snapshot.Buckets = appendPercentBucket(snapshot.Buckets, "total", "Combined usage", usage.TotalPercentUsed, resetsAt)
	if usage.TotalSpend.Valid || usage.Limit.Valid || usage.IncludedSpend.Valid || usage.BonusSpend.Valid {
		bucket := providerlimits.Bucket{
			ID:       "spend",
			Label:    "Spend pools",
			Unit:     providerlimits.UnitCurrency,
			ResetsAt: copyTime(resetsAt),
			Status:   providerlimits.StatusOK,
			Note:     spendNote(usage),
		}
		if usage.TotalSpend.Valid {
			bucket.UsedValue = numberPointer(usage.TotalSpend.Value / 100)
		}
		if usage.Limit.Valid {
			bucket.LimitValue = numberPointer(usage.Limit.Value / 100)
			if usage.TotalSpend.Valid {
				bucket.RemainingValue = numberPointer(math.Max(0, usage.Limit.Value-usage.TotalSpend.Value) / 100)
			}
		}
		snapshot.Buckets = append(snapshot.Buckets, bucket)
	}
	if len(snapshot.Buckets) == 0 {
		snapshot.Status = providerlimits.StatusUnavailable
		snapshot.ErrorNote = "usage_unavailable"
	}
	return snapshot
}

func mergePlanUsage(response usageResponse) planUsage {
	usage := response.PlanUsage
	usage.AutoPercentUsed = preferNumber(usage.AutoPercentUsed, response.AutoPercentUsed)
	usage.APIPercentUsed = preferNumber(usage.APIPercentUsed, response.APIPercentUsed)
	usage.TotalPercentUsed = preferNumber(usage.TotalPercentUsed, response.TotalPercentUsed)
	usage.TotalSpend = preferNumber(usage.TotalSpend, response.TotalSpend)
	usage.IncludedSpend = preferNumber(usage.IncludedSpend, response.IncludedSpend)
	usage.BonusSpend = preferNumber(usage.BonusSpend, response.BonusSpend)
	usage.Limit = preferNumber(usage.Limit, response.Limit)
	return usage
}

func preferNumber(primary, fallback flexibleNumber) flexibleNumber {
	if primary.Valid {
		return primary
	}
	return fallback
}

func appendPercentBucket(output []providerlimits.Bucket, id, label string, value flexibleNumber, resetsAt *time.Time) []providerlimits.Bucket {
	if !value.Valid || value.Value < 0 || value.Value > 100 {
		return output
	}
	return append(output, providerlimits.Bucket{
		ID:             id,
		Label:          label,
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(value.Value),
		RemainingValue: numberPointer(100 - value.Value),
		ResetsAt:       copyTime(resetsAt),
		Status:         providerlimits.StatusOK,
	})
}

func spendNote(usage planUsage) string {
	parts := make([]string, 0, 2)
	if usage.IncludedSpend.Valid {
		parts = append(parts, "included_"+strconv.FormatInt(int64(usage.IncludedSpend.Value), 10))
	}
	if usage.BonusSpend.Valid {
		parts = append(parts, "bonus_"+strconv.FormatInt(int64(usage.BonusSpend.Value), 10))
	}
	return strings.Join(parts, "_")
}

func accountKeyFrom(subject, email string) string {
	identity := strings.TrimSpace(subject) + "|" + strings.ToLower(strings.TrimSpace(email))
	if identity == "|" {
		return "unavailable"
	}
	hash := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(hash[:])[:16]
}

func maskEmail(email string) string {
	parts := strings.Split(strings.TrimSpace(email), "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return string([]rune(parts[0])[0]) + "***@" + parts[1]
}

func (a *Adapter) storeLastGood(snapshot providerlimits.AccountSnapshot) {
	a.mu.Lock()
	copied := copySnapshot(snapshot)
	a.lastGood = &copied
	a.mu.Unlock()
}

func (a *Adapter) staleOrUnavailable(checkedAt time.Time, reason string) []providerlimits.AccountSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastGood == nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, reason)}
	}
	copied := copySnapshot(*a.lastGood)
	copied.CheckedAt = checkedAt
	copied.Status = providerlimits.StatusStale
	copied.ErrorNote = reason
	for index := range copied.Buckets {
		copied.Buckets[index].Status = providerlimits.StatusStale
	}
	return []providerlimits.AccountSnapshot{copied}
}

func unavailableSnapshot(checkedAt time.Time, reason string) providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:   "cursor",
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     providerlimits.StatusUnavailable,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalAuthState,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		ErrorNote: reason,
	}
}

func copySnapshot(snapshot providerlimits.AccountSnapshot) providerlimits.AccountSnapshot {
	copied := snapshot
	copied.Buckets = append([]providerlimits.Bucket(nil), snapshot.Buckets...)
	for index, bucket := range copied.Buckets {
		copied.Buckets[index] = bucket
		copied.Buckets[index].LimitValue = copyNumber(bucket.LimitValue)
		copied.Buckets[index].UsedValue = copyNumber(bucket.UsedValue)
		copied.Buckets[index].RemainingValue = copyNumber(bucket.RemainingValue)
		copied.Buckets[index].ResetsAt = copyTime(bucket.ResetsAt)
	}
	return copied
}

func defaultStateDBPath() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Cursor", "User", "globalStorage", "state.vscdb")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	}
}

func timeFromJSON(raw json.RawMessage) *time.Time {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func numberPointer(value float64) *float64 {
	return &value
}

func copyNumber(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

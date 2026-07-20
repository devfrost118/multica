// Package codex reads Codex CLI OAuth state locally and obtains normalized
// subscription limits from the same ChatGPT backend endpoint the Codex CLI's
// usage screen queries. Credentials and raw endpoint payloads never leave
// this package.
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

const (
	defaultEndpoint  = "https://chatgpt.com/backend-api/wham/usage"
	defaultFreshness = 15 * time.Minute
)

// ErrRateLimited deliberately contains no provider response detail. Returning
// it with a stale snapshot lets the collector retain the last useful reading
// while applying its ordinary provider backoff.
var ErrRateLimited = errors.New("codex usage rate limited")

// Config supplies testable local and HTTP dependencies. An empty Home honors
// CODEX_HOME and otherwise falls back to ~/.codex.
type Config struct {
	Home     string
	Endpoint string
	Client   *http.Client
	Now      func() time.Time
}

// Adapter is a daemon-local Codex subscription-limit adapter.
type Adapter struct {
	home     string
	endpoint string
	client   *http.Client
	now      func() time.Time

	mu       sync.Mutex
	lastGood *providerlimits.AccountSnapshot
}

// NewAdapter constructs an adapter without touching local auth state.
func NewAdapter(config Config) *Adapter {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Adapter{
		home:     resolveHome(config.Home),
		endpoint: endpoint,
		client:   client,
		now:      now,
	}
}

func (*Adapter) Provider() string { return "codex" }

func (*Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 5 * time.Second, MinimumInterval: defaultFreshness}
}

// Collect probes the ChatGPT backend usage endpoint with the local OAuth
// access token from ~/.codex/auth.json. Missing auth state and unauthorized
// responses are normal unavailable outcomes, not transport errors. Refresh
// tokens are intentionally ignored, matching the Claude adapter's approach.
func (a *Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	checkedAt := a.now().UTC()
	accessToken, ok := a.loadAccessToken()
	if !ok {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("codex usage request unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("codex usage request unavailable")
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	case http.StatusTooManyRequests:
		return a.staleOrUnavailable(checkedAt), ErrRateLimited
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("codex usage request unavailable")
	}

	var usage usageResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&usage); err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("codex usage response unavailable")
	}
	snapshot := snapshotFromUsage(usage, checkedAt)
	if len(snapshot.Buckets) == 0 {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	}
	a.storeLastGood(snapshot)
	return []providerlimits.AccountSnapshot{snapshot}, nil
}

// authFile mirrors the OAuth tokens Codex CLI writes to ~/.codex/auth.json.
type authFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func (a *Adapter) loadAccessToken() (string, bool) {
	contents, err := os.ReadFile(filepath.Join(a.home, "auth.json"))
	if err != nil {
		return "", false
	}
	var auth authFile
	if err := json.Unmarshal(contents, &auth); err != nil {
		return "", false
	}
	accessToken := strings.TrimSpace(auth.Tokens.AccessToken)
	if accessToken == "" {
		return "", false
	}
	return accessToken, true
}

// usageResponse mirrors the real shape of the ChatGPT backend usage endpoint
// (GET /backend-api/wham/usage), the same endpoint the Codex CLI's usage
// screen queries. primary_window is the short session window; secondary_window
// is the weekly window.
type usageResponse struct {
	AccountID string          `json:"account_id"`
	Email     string          `json:"email"`
	PlanType  string          `json:"plan_type"`
	RateLimit *usageRateLimit `json:"rate_limit"`
}

type usageRateLimit struct {
	PrimaryWindow   *usageWindow `json:"primary_window"`
	SecondaryWindow *usageWindow `json:"secondary_window"`
}

type usageWindow struct {
	UsedPercent json.RawMessage `json:"used_percent"`
	ResetAt     json.RawMessage `json:"reset_at"`
}

func snapshotFromUsage(usage usageResponse, checkedAt time.Time) providerlimits.AccountSnapshot {
	buckets := make([]providerlimits.Bucket, 0, 2)
	if usage.RateLimit != nil {
		if bucket, ok := bucketFromWindow("session", "Limit session", usage.RateLimit.PrimaryWindow); ok {
			buckets = append(buckets, bucket)
		}
		if bucket, ok := bucketFromWindow("weekly", "Limit weekly", usage.RateLimit.SecondaryWindow); ok {
			buckets = append(buckets, bucket)
		}
	}
	return providerlimits.AccountSnapshot{
		Provider:     "codex",
		AccountKey:   accountKeyFrom(usage.AccountID, usage.Email),
		AccountLabel: providerlimits.NormalizeProfileLabel(usage.PlanType),
		CheckedAt:    checkedAt,
		Status:       providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindOfficialAPI,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		Buckets: buckets,
	}
}

func bucketFromWindow(id, label string, window *usageWindow) (providerlimits.Bucket, bool) {
	if window == nil {
		return providerlimits.Bucket{}, false
	}
	used, ok := numberFromJSON(window.UsedPercent)
	if !ok || used < 0 || used > 100 {
		return providerlimits.Bucket{}, false
	}
	resetsAt, _ := timeFromJSON(window.ResetAt)
	return providerlimits.Bucket{
		ID:             id,
		Label:          label,
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(used),
		RemainingValue: numberPointer(100 - used),
		ResetsAt:       resetsAt,
		Status:         providerlimits.StatusOK,
	}, true
}

// accountKeyFrom derives a stable, non-reversible account key from whichever
// real identifier the endpoint returned. It intentionally never crosses the
// package boundary as anything but a hash prefix.
func accountKeyFrom(accountID, email string) string {
	identity := strings.TrimSpace(accountID)
	if identity == "" {
		identity = strings.ToLower(strings.TrimSpace(email))
	}
	if identity == "" {
		return "unavailable"
	}
	hash := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(hash[:])[:16]
}

func (a *Adapter) staleOrUnavailable(checkedAt time.Time) []providerlimits.AccountSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastGood == nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}
	}
	stale := staleSnapshot(*a.lastGood, checkedAt)
	return []providerlimits.AccountSnapshot{stale}
}

func (a *Adapter) storeLastGood(snapshot providerlimits.AccountSnapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	copied := copySnapshot(snapshot)
	a.lastGood = &copied
}

func staleSnapshot(snapshot providerlimits.AccountSnapshot, checkedAt time.Time) providerlimits.AccountSnapshot {
	copied := copySnapshot(snapshot)
	copied.CheckedAt = checkedAt
	copied.Status = providerlimits.StatusStale
	copied.ErrorNote = "rate_limited"
	for index := range copied.Buckets {
		copied.Buckets[index].Status = providerlimits.StatusStale
	}
	return copied
}

func copySnapshot(snapshot providerlimits.AccountSnapshot) providerlimits.AccountSnapshot {
	copied := snapshot
	copied.Buckets = make([]providerlimits.Bucket, len(snapshot.Buckets))
	for index, bucket := range snapshot.Buckets {
		copied.Buckets[index] = bucket
		if bucket.LimitValue != nil {
			value := *bucket.LimitValue
			copied.Buckets[index].LimitValue = &value
		}
		if bucket.UsedValue != nil {
			value := *bucket.UsedValue
			copied.Buckets[index].UsedValue = &value
		}
		if bucket.RemainingValue != nil {
			value := *bucket.RemainingValue
			copied.Buckets[index].RemainingValue = &value
		}
		if bucket.ResetsAt != nil {
			value := *bucket.ResetsAt
			copied.Buckets[index].ResetsAt = &value
		}
	}
	return copied
}

func unavailableSnapshot(checkedAt time.Time) providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:   "codex",
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     providerlimits.StatusUnavailable,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindOfficialAPI,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		ErrorNote: "usage_unavailable",
	}
}

func resolveHome(configured string) string {
	if home := strings.TrimSpace(configured); home != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func numberFromJSON(raw json.RawMessage) (float64, bool) {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func timeFromJSON(raw json.RawMessage) (*time.Time, bool) {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		return nil, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		if seconds > 100_000_000_000 {
			seconds /= 1_000
		}
		parsed := time.Unix(seconds, 0).UTC()
		return &parsed, true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func numberPointer(value float64) *float64 {
	return &value
}

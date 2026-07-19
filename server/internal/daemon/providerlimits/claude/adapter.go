// Package claude reads Claude Code OAuth state locally and obtains normalized
// subscription limits from Anthropic's official usage endpoint. Credentials
// and raw endpoint payloads never leave this package. The MVP supports the
// Windows/Linux credential file only; macOS Keychain-backed auth reports
// unavailable until a dedicated Keychain reader is implemented.
package claude

import (
	"context"
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
	defaultEndpoint  = "https://api.anthropic.com/api/oauth/usage"
	defaultFreshness = 15 * time.Minute
)

// ErrRateLimited deliberately contains no provider response detail. Returning
// it with a stale snapshot lets the collector retain the last useful reading
// while applying its ordinary provider backoff.
var ErrRateLimited = errors.New("claude usage rate limited")

// Config supplies testable local and HTTP dependencies. An empty ConfigDir
// honors CLAUDE_CONFIG_DIR and otherwise falls back to ~/.claude.
type Config struct {
	ConfigDir string
	Endpoint  string
	Client    *http.Client
	Now       func() time.Time
}

// Adapter is a daemon-local Claude subscription-limit adapter.
type Adapter struct {
	configDir string
	endpoint  string
	client    *http.Client
	now       func() time.Time

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
		configDir: resolveConfigDir(config.ConfigDir),
		endpoint:  endpoint,
		client:    client,
		now:       now,
	}
}

func (*Adapter) Provider() string { return "claude" }

func (*Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 5 * time.Second, MinimumInterval: defaultFreshness}
}

// Collect probes the official usage endpoint with an unexpired local access
// token. Missing, expired, and unauthorized auth state are normal unavailable
// outcomes, not transport errors. Refresh tokens are intentionally ignored.
func (a *Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	checkedAt := a.now().UTC()
	accessToken, ok := a.loadAccessToken(checkedAt)
	if !ok {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("claude usage request unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := a.client.Do(request)
	if err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("claude usage request unavailable")
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	case http.StatusTooManyRequests:
		return a.staleOrUnavailable(checkedAt), ErrRateLimited
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("claude usage request unavailable")
	}

	var usage usageResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&usage); err != nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, errors.New("claude usage response unavailable")
	}
	snapshot := snapshotFromUsage(usage, checkedAt)
	if len(snapshot.Buckets) == 0 {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt)}, nil
	}
	a.storeLastGood(snapshot)
	return []providerlimits.AccountSnapshot{snapshot}, nil
}

type credentialsFile struct {
	ClaudeAIOAuth struct {
		AccessToken string          `json:"accessToken"`
		ExpiresAt   json.RawMessage `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

func (a *Adapter) loadAccessToken(now time.Time) (string, bool) {
	contents, err := os.ReadFile(filepath.Join(a.configDir, ".credentials.json"))
	if err != nil {
		return "", false
	}
	var credentials credentialsFile
	if err := json.Unmarshal(contents, &credentials); err != nil {
		return "", false
	}
	accessToken := strings.TrimSpace(credentials.ClaudeAIOAuth.AccessToken)
	expiresAt, ok := parseExpiry(credentials.ClaudeAIOAuth.ExpiresAt)
	if accessToken == "" || !ok || !expiresAt.After(now) {
		return "", false
	}
	return accessToken, true
}

func parseExpiry(raw json.RawMessage) (time.Time, bool) {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		return time.Time{}, false
	}
	if timestamp, err := strconv.ParseInt(value, 10, 64); err == nil && timestamp > 0 {
		if timestamp > 100_000_000_000 {
			timestamp /= 1_000
		}
		return time.Unix(timestamp, 0).UTC(), true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

type usageResponse struct {
	FiveHour   *usageWindow `json:"five_hour"`
	SevenDay   *usageWindow `json:"seven_day"`
	Limits     []usageLimit `json:"limits"`
	ExtraUsage *usageWindow `json:"extra_usage"`
	Spend      *usageWindow `json:"spend"`
}

type usageWindow struct {
	Utilization json.RawMessage `json:"utilization"`
	Percent     json.RawMessage `json:"percent"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

type usageLimit struct {
	Kind     string          `json:"kind"`
	Percent  json.RawMessage `json:"percent"`
	ResetsAt json.RawMessage `json:"resets_at"`
}

func snapshotFromUsage(usage usageResponse, checkedAt time.Time) providerlimits.AccountSnapshot {
	buckets := make([]providerlimits.Bucket, 0, 4+len(usage.Limits))
	for _, window := range []struct {
		id     string
		label  string
		window *usageWindow
	}{
		{id: "five_hour", label: "Five hour", window: usage.FiveHour},
		{id: "seven_day", label: "Seven day", window: usage.SevenDay},
	} {
		if bucket, ok := bucketFromWindow(window.id, window.label, window.window); ok {
			buckets = append(buckets, bucket)
		}
	}
	for _, limit := range usage.Limits {
		kind := normalizeIdentifier(limit.Kind)
		if kind == "" {
			continue
		}
		bucket, ok := percentBucket("limit-"+kind, "Limit "+strings.ReplaceAll(kind, "_", " "), limit.Percent, limit.ResetsAt)
		if ok {
			buckets = append(buckets, bucket)
		}
	}
	for _, window := range []struct {
		id     string
		label  string
		window *usageWindow
	}{
		{id: "extra_usage", label: "Extra usage", window: usage.ExtraUsage},
		{id: "spend", label: "Spend", window: usage.Spend},
	} {
		if bucket, ok := bucketFromWindow(window.id, window.label, window.window); ok {
			buckets = append(buckets, bucket)
		}
	}
	return providerlimits.AccountSnapshot{
		Provider:   "claude",
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalAuthState,
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
	percent := window.Utilization
	if len(percent) == 0 {
		percent = window.Percent
	}
	return percentBucket(id, label, percent, window.ResetsAt)
}

func percentBucket(id, label string, percentRaw, resetRaw json.RawMessage) (providerlimits.Bucket, bool) {
	percent, ok := numberFromJSON(percentRaw)
	if !ok || percent < 0 || percent > 100 {
		return providerlimits.Bucket{}, false
	}
	resetsAt, _ := timeFromJSON(resetRaw)
	return providerlimits.Bucket{
		ID:             id,
		Label:          label,
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(percent),
		RemainingValue: numberPointer(100 - percent),
		ResetsAt:       resetsAt,
		Status:         providerlimits.StatusOK,
	}, true
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
		Provider:   "claude",
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     providerlimits.StatusUnavailable,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalAuthState,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		ErrorNote: "usage_unavailable",
	}
}

func resolveConfigDir(configured string) string {
	if path := strings.TrimSpace(configured); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func normalizeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9', character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteByte('_')
		}
	}
	normalized := strings.Trim(builder.String(), "_-")
	if len(normalized) > 48 {
		normalized = normalized[:48]
	}
	return normalized
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

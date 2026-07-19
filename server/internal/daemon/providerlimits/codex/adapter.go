// Package codex reads normalized subscription limits from local Codex session
// logs. It never returns, stores, or logs raw session content.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

const (
	defaultFreshness = 15 * time.Minute
	maxJSONLLineSize = 1 << 20
)

// Config configures the local Codex session location. An empty Home honors
// CODEX_HOME and falls back to ~/.codex.
type Config struct {
	Home string
}

// Adapter provides observed Codex subscription-limit snapshots.
type Adapter struct {
	home string
}

// NewAdapter creates a local-log adapter without reading the filesystem.
func NewAdapter(config Config) Adapter {
	return Adapter{home: resolveHome(config.Home)}
}

func (Adapter) Provider() string { return "codex" }

func (Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 5 * time.Second, MinimumInterval: defaultFreshness}
}

// Collect scans JSONL files incrementally and returns the most recent valid
// rate_limits event. checked_at deliberately remains the event timestamp so
// backend staleness reflects the latest Codex activity, not collection time.
func (a Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	latest, found, err := latestRateLimitsEvent(ctx, filepath.Join(a.home, "sessions"))
	if err != nil {
		return nil, err
	}
	if !found {
		return []providerlimits.AccountSnapshot{unavailableSnapshot()}, nil
	}
	return []providerlimits.AccountSnapshot{snapshotFromEvent(latest)}, nil
}

type sessionEvent struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		RateLimits json.RawMessage `json:"rate_limits"`
		Type       string          `json:"type"`
	} `json:"payload"`
}

type parsedRateLimitsEvent struct {
	checkedAt time.Time
	limits    rawRateLimits
}

type rawRateLimits struct {
	Primary   rawWindow                  `json:"primary"`
	Secondary rawWindow                  `json:"secondary"`
	Credits   map[string]json.RawMessage `json:"credits"`
	PlanType  string                     `json:"plan_type"`
}

type rawWindow struct {
	UsedPercent   json.RawMessage `json:"used_percent"`
	WindowMinutes json.RawMessage `json:"window_minutes"`
	ResetsAt      json.RawMessage `json:"resets_at"`
}

func latestRateLimitsEvent(ctx context.Context, sessionsDir string) (parsedRateLimitsEvent, bool, error) {
	latest := parsedRateLimitsEvent{}
	found := false

	err := filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate, ok, fileErr := latestEventInFile(ctx, path)
		if fileErr != nil {
			if ctx.Err() != nil || errors.Is(fileErr, context.Canceled) || errors.Is(fileErr, context.DeadlineExceeded) {
				return fileErr
			}
			return nil
		}
		if !ok {
			return nil
		}
		if !found || candidate.checkedAt.After(latest.checkedAt) {
			latest = candidate
			found = true
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return parsedRateLimitsEvent{}, false, ctx.Err()
		}
		return parsedRateLimitsEvent{}, false, nil
	}
	return latest, found, nil
}

func latestEventInFile(ctx context.Context, path string) (parsedRateLimitsEvent, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return parsedRateLimitsEvent{}, false, err
	}
	defer file.Close()

	latest := parsedRateLimitsEvent{}
	found := false
	reader := bufio.NewReaderSize(file, 64*1024)
	line := make([]byte, 0, 64*1024)
	discardingLine := false
	for {
		if err := ctx.Err(); err != nil {
			return parsedRateLimitsEvent{}, false, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		if !discardingLine {
			if len(line)+len(fragment) > maxJSONLLineSize {
				line = line[:0]
				discardingLine = true
			} else {
				line = append(line, fragment...)
			}
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if !discardingLine {
			candidate, ok := parseRateLimitsLine(line)
			if ok && (!found || candidate.checkedAt.After(latest.checkedAt)) {
				latest = candidate
				found = true
			}
		}
		line = line[:0]
		discardingLine = false
		switch {
		case readErr == nil:
			continue
		case errors.Is(readErr, io.EOF):
			return latest, found, nil
		default:
			return parsedRateLimitsEvent{}, false, readErr
		}
	}
}

func parseRateLimitsLine(line []byte) (parsedRateLimitsEvent, bool) {
	var event sessionEvent
	if err := json.Unmarshal(line, &event); err != nil || event.Type != "event_msg" || event.Payload.Type != "token_count" || len(event.Payload.RateLimits) == 0 || string(event.Payload.RateLimits) == "null" {
		return parsedRateLimitsEvent{}, false
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return parsedRateLimitsEvent{}, false
	}
	var limits rawRateLimits
	if err := json.Unmarshal(event.Payload.RateLimits, &limits); err != nil {
		return parsedRateLimitsEvent{}, false
	}
	if !hasUsableQuotaData(limits) {
		return parsedRateLimitsEvent{}, false
	}
	return parsedRateLimitsEvent{checkedAt: checkedAt.UTC(), limits: limits}, true
}

func hasUsableQuotaData(limits rawRateLimits) bool {
	if _, ok := bucketFromWindow("primary", "Primary", limits.Primary); ok {
		return true
	}
	if _, ok := bucketFromWindow("secondary", "Secondary", limits.Secondary); ok {
		return true
	}
	_, ok := bucketFromCredits(limits.Credits)
	return ok
}

func snapshotFromEvent(event parsedRateLimitsEvent) providerlimits.AccountSnapshot {
	buckets := make([]providerlimits.Bucket, 0, 3)
	status := providerlimits.StatusOK
	for _, window := range []struct {
		id    string
		label string
		value rawWindow
	}{
		{id: "primary", label: "Primary", value: event.limits.Primary},
		{id: "secondary", label: "Secondary", value: event.limits.Secondary},
	} {
		bucket, ok := bucketFromWindow(window.id, window.label, window.value)
		if !ok {
			status = providerlimits.StatusPartial
			continue
		}
		if bucket.Status != providerlimits.StatusOK {
			status = providerlimits.StatusPartial
		}
		buckets = append(buckets, bucket)
	}
	if credits, ok := bucketFromCredits(event.limits.Credits); ok {
		if credits.Status != providerlimits.StatusOK {
			status = providerlimits.StatusPartial
		}
		buckets = append(buckets, credits)
	}
	if len(buckets) == 0 {
		status = providerlimits.StatusPartial
	}

	snapshot := providerlimits.AccountSnapshot{
		Provider:     "codex",
		AccountKey:   "unavailable",
		AccountLabel: normalizePlanLabel(event.limits.PlanType),
		CheckedAt:    event.checkedAt,
		Status:       status,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalLog,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceObserved,
		},
		Buckets: buckets,
	}
	if status == providerlimits.StatusPartial {
		snapshot.ErrorNote = "rate_limits_partial"
	}
	return snapshot
}

func bucketFromWindow(id, baseLabel string, window rawWindow) (providerlimits.Bucket, bool) {
	used, usedOK := numberFromJSON(window.UsedPercent)
	if !usedOK || used < 0 || used > 100 {
		return providerlimits.Bucket{}, false
	}
	remaining := 100 - used
	minutes, minutesOK := positiveIntFromJSON(window.WindowMinutes)
	resetsAt, resetsOK := timeFromJSON(window.ResetsAt)
	status := providerlimits.StatusOK
	if !minutesOK || !resetsOK {
		status = providerlimits.StatusPartial
	}
	return providerlimits.Bucket{
		ID:             id,
		Label:          windowLabel(baseLabel, minutes, minutesOK),
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(used),
		RemainingValue: numberPointer(remaining),
		ResetsAt:       resetsAt,
		Status:         status,
	}, true
}

func bucketFromCredits(credits map[string]json.RawMessage) (providerlimits.Bucket, bool) {
	if len(credits) == 0 {
		return providerlimits.Bucket{}, false
	}
	if unlimited, ok := boolFromJSON(credits["unlimited"]); ok && unlimited {
		return providerlimits.Bucket{ID: "credits", Label: "Credits", Unit: providerlimits.UnitCredits, Status: providerlimits.StatusOK, Note: "unlimited"}, true
	}
	balance, ok := numberFromJSON(credits["balance"])
	if !ok || balance < 0 {
		return providerlimits.Bucket{}, false
	}
	return providerlimits.Bucket{ID: "credits", Label: "Credits", Unit: providerlimits.UnitCredits, RemainingValue: numberPointer(balance), Status: providerlimits.StatusOK}, true
}

func unavailableSnapshot() providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:   "codex",
		AccountKey: "unavailable",
		CheckedAt:  time.Now().UTC(),
		Status:     providerlimits.StatusUnavailable,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindLocalLog,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceObserved,
		},
		ErrorNote: "rate_limits_unavailable",
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

func normalizePlanLabel(planType string) string {
	planType = strings.ToLower(strings.TrimSpace(planType))
	if planType == "" {
		return ""
	}
	var builder strings.Builder
	previousDash := false
	for _, character := range planType {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9', character == '_', character == '-':
			builder.WriteRune(character)
			previousDash = false
		default:
			if !previousDash {
				builder.WriteByte('-')
				previousDash = true
			}
		}
	}
	normalized := strings.Trim(builder.String(), "-_")
	if normalized == "" {
		return ""
	}
	if len(normalized) > 48 {
		normalized = normalized[:48]
	}
	return "profile-" + normalized
}

func windowLabel(base string, minutes int64, valid bool) string {
	if !valid {
		return base
	}
	switch {
	case minutes%1_440 == 0:
		return base + " " + strconv.FormatInt(minutes/1_440, 10) + "d"
	case minutes%60 == 0:
		return base + " " + strconv.FormatInt(minutes/60, 10) + "h"
	default:
		return base + " " + strconv.FormatInt(minutes, 10) + "m"
	}
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

func positiveIntFromJSON(raw json.RawMessage) (int64, bool) {
	number, ok := numberFromJSON(raw)
	if !ok || number <= 0 || number != math.Trunc(number) || number > math.MaxInt64 {
		return 0, false
	}
	return int64(number), true
}

func timeFromJSON(raw json.RawMessage) (*time.Time, bool) {
	if seconds, ok := positiveIntFromJSON(raw); ok {
		if seconds > 100_000_000_000 {
			seconds /= 1_000
		}
		parsed := time.Unix(seconds, 0).UTC()
		return &parsed, true
	}
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func boolFromJSON(raw json.RawMessage) (bool, bool) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func numberPointer(value float64) *float64 {
	return &value
}

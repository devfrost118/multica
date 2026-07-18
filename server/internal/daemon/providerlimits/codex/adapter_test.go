package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

func TestAdapter_CollectsLatestValidRateLimitsEventAcrossSessionFiles(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeSessionFile(t, home, "2026/07/older.jsonl", strings.Join([]string{
		"{malformed-json",
		rateLimitsEvent("2026-07-17T10:00:00Z", 20, 300, 1_784_358_000, 40, 10_080, 1_784_358_000, `{"balance":"12.5","unlimited":false}`, "plus"),
	}, "\n"))
	writeSessionFile(t, home, "2026/07/newer.jsonl", rateLimitsEvent("2026-07-17T12:00:00Z", 25, 300, 1_784_365_200, 50, 10_080, 1_784_965_200, `{"balance":"9","unlimited":false}`, "pro"))

	snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Provider != "codex" || snapshot.AccountKey != "unavailable" || snapshot.AccountLabel != "profile-pro" {
		t.Fatalf("identity = %#v", snapshot)
	}
	if want := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC); !snapshot.CheckedAt.Equal(want) {
		t.Fatalf("checked_at = %s, want %s", snapshot.CheckedAt, want)
	}
	if snapshot.Status != providerlimits.StatusOK || snapshot.Source.Kind != providerlimits.SourceKindLocalLog || snapshot.Source.Confidence != providerlimits.ConfidenceObserved {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if snapshot.Source.FreshnessSeconds != 15*60 {
		t.Fatalf("freshness_seconds = %d, want 900", snapshot.Source.FreshnessSeconds)
	}
	if len(snapshot.Buckets) != 3 {
		t.Fatalf("bucket count = %d, want 3", len(snapshot.Buckets))
	}

	assertPercentBucket(t, snapshot.Buckets[0], "primary", "Primary 5h", 25, 75, time.Unix(1_784_365_200, 0).UTC())
	assertPercentBucket(t, snapshot.Buckets[1], "secondary", "Secondary 7d", 50, 50, time.Unix(1_784_965_200, 0).UTC())
	credits := snapshot.Buckets[2]
	if credits.ID != "credits" || credits.Unit != providerlimits.UnitCredits || credits.RemainingValue == nil || *credits.RemainingValue != 9 {
		t.Fatalf("credits bucket = %#v", credits)
	}
}

func TestAdapter_SkipsNewerRateLimitsEventWithoutUsableQuotaData(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeSessionFile(t, home, "older.jsonl", rateLimitsEvent("2026-07-17T12:00:00Z", 25, 300, 1_784_365_200, 50, 10_080, 1_784_965_200, "null", "plus"))
	writeSessionFile(t, home, "newer-invalid.jsonl", `{"timestamp":"2026-07-17T13:00:00Z","payload":{"rate_limits":{"primary":{"used_percent":"not-a-number"}}}}`)

	snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if want := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC); !snapshots[0].CheckedAt.Equal(want) {
		t.Fatalf("checked_at = %s, want previous valid event at %s", snapshots[0].CheckedAt, want)
	}
}

func TestAdapter_SkipsOversizedMalformedLineAndContinuesScanningFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeSessionFile(t, home, "session.jsonl", strings.Repeat("x", maxJSONLLineSize+1)+"\n"+rateLimitsEvent("2026-07-17T12:00:00Z", 25, 300, 1_784_365_200, 50, 10_080, 1_784_965_200, "null", "plus"))

	snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusOK {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestAdapter_DoesNotTreatCreditsWithoutBalanceAsUsableQuotaData(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeSessionFile(t, home, "older.jsonl", rateLimitsEvent("2026-07-17T12:00:00Z", 25, 300, 1_784_365_200, 50, 10_080, 1_784_965_200, "null", "plus"))
	writeSessionFile(t, home, "newer-credits-only.jsonl", `{"timestamp":"2026-07-17T13:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"credits":{"unlimited":false}}}}`)

	snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if want := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC); !snapshots[0].CheckedAt.Equal(want) {
		t.Fatalf("checked_at = %s, want previous valid event at %s", snapshots[0].CheckedAt, want)
	}
}

func TestAdapter_UsesCodexHomeAndHandlesCyrillicWindowsPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "\u041a\u043e\u0434\u0435\u043a\u0441", "\u043f\u0440\u043e\u0444\u0438\u043b\u044c")
	t.Setenv("CODEX_HOME", home)
	writeSessionFile(t, home, "2026/07/session.jsonl", rateLimitsEvent("2026-07-17T12:00:00Z", 1, 60, 1_784_365_200, 2, 120, 1_784_365_200, "null", "free"))

	snapshots, err := NewAdapter(Config{}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusOK || snapshots[0].AccountLabel != "profile-free" {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestAdapter_ReturnsUnavailableForMissingOrInvalidSessionsWithoutLeakingLogContent(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		setup func(t *testing.T, home string)
	}{
		{name: "missing directory", setup: func(*testing.T, string) {}},
		{name: "empty file", setup: func(t *testing.T, home string) { writeSessionFile(t, home, "empty.jsonl", "") }},
		{name: "malformed lines", setup: func(t *testing.T, home string) {
			writeSessionFile(t, home, "bad.jsonl", "{not json}\n{\"prompt\":\"must-not-cross-boundary\"}")
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			testCase.setup(t, home)

			snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusUnavailable || snapshots[0].ErrorNote != "rate_limits_unavailable" {
				t.Fatalf("snapshots = %#v", snapshots)
			}
			encoded, marshalErr := json.Marshal(snapshots)
			if marshalErr != nil {
				t.Fatalf("marshal snapshots: %v", marshalErr)
			}
			if strings.Contains(string(encoded), "must-not-cross-boundary") {
				t.Fatalf("raw log content crossed snapshot boundary: %s", encoded)
			}
		})
	}
}

func TestAdapter_PreservesOldEventTimestampForBackendStaleness(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	old := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	writeSessionFile(t, home, "stale.jsonl", rateLimitsEvent(old.Format(time.RFC3339), 1, 60, 1_784_365_200, 2, 120, 1_784_365_200, "null", "plus"))

	snapshots, err := NewAdapter(Config{Home: home}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || !snapshots[0].CheckedAt.Equal(old) || snapshots[0].Status != providerlimits.StatusOK {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func assertPercentBucket(t *testing.T, bucket providerlimits.Bucket, id, label string, used, remaining float64, resetsAt time.Time) {
	t.Helper()
	if bucket.ID != id || bucket.Label != label || bucket.Unit != providerlimits.UnitPercent || bucket.LimitValue == nil || *bucket.LimitValue != 100 || bucket.UsedValue == nil || *bucket.UsedValue != used || bucket.RemainingValue == nil || *bucket.RemainingValue != remaining || bucket.ResetsAt == nil || !bucket.ResetsAt.Equal(resetsAt) || bucket.Status != providerlimits.StatusOK {
		t.Fatalf("bucket %s = %#v", id, bucket)
	}
}

func writeSessionFile(t *testing.T, home, relativePath, content string) {
	t.Helper()
	path := filepath.Join(home, "sessions", relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create session directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write session fixture: %v", err)
	}
}

func rateLimitsEvent(timestamp string, primaryUsed float64, primaryWindow int64, primaryReset int64, secondaryUsed float64, secondaryWindow int64, secondaryReset int64, credits, planType string) string {
	return `{"timestamp":` + quoteJSON(timestamp) + `,"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":` + numberJSON(primaryUsed) + `,"window_minutes":` + numberJSON(primaryWindow) + `,"resets_at":` + numberJSON(primaryReset) + `},"secondary":{"used_percent":` + numberJSON(secondaryUsed) + `,"window_minutes":` + numberJSON(secondaryWindow) + `,"resets_at":` + numberJSON(secondaryReset) + `},"credits":` + credits + `,"plan_type":` + quoteJSON(planType) + `}}}`
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func numberJSON[T int64 | float64](value T) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

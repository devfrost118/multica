package claude

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

func TestAdapter_CollectsOfficialUsageAndIgnoresUnknownFields(t *testing.T) {
	var authorization, beta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		beta = r.Header.Get("anthropic-beta")
		if r.URL.Path != "/api/oauth/usage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour":{"utilization":25,"resets_at":"2026-07-19T18:00:00Z","unknown":true},
			"seven_day":{"utilization":"40","resets_at":"2026-07-25T18:00:00Z"},
			"seven_day_opus":{"utilization":35,"resets_at":"2026-07-25T18:00:00Z"},
			"seven_day_sonnet":{"utilization":10,"resets_at":"2026-07-25T18:00:00Z"},
			"future_field":"sk-response-token-must-not-leak"
		}`))
	}))
	defer server.Close()

	now := time.Date(2026, time.July, 19, 17, 0, 0, 0, time.UTC)
	configDir := writeCredentials(t, "test-access-token-must-not-leak", now.Add(time.Hour))
	snapshots, err := NewAdapter(Config{ConfigDir: configDir, Endpoint: server.URL + "/api/oauth/usage", Now: func() time.Time { return now }}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if authorization != "Bearer test-access-token-must-not-leak" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if beta != "oauth-2025-04-20" {
		t.Fatalf("anthropic-beta = %q", beta)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Provider != "claude" || snapshot.AccountKey != "unavailable" || !snapshot.CheckedAt.Equal(now) {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.AccountLabel != "profile-max" {
		t.Fatalf("snapshot account label = %q, want profile-max", snapshot.AccountLabel)
	}
	if snapshot.Status != providerlimits.StatusOK || snapshot.Source.Kind != providerlimits.SourceKindLocalAuthState || snapshot.Source.Confidence != providerlimits.ConfidenceOfficial {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if len(snapshot.Buckets) != 3 {
		t.Fatalf("bucket count = %d, want 3: %#v", len(snapshot.Buckets), snapshot.Buckets)
	}
	assertPercentBucket(t, snapshot.Buckets[0], "session", "Limit session", 25, 75, "2026-07-19T18:00:00Z")
	assertPercentBucket(t, snapshot.Buckets[1], "weekly_all", "Limit weekly all", 40, 60, "2026-07-25T18:00:00Z")
	assertPercentBucket(t, snapshot.Buckets[2], "weekly_scoped", "Limit weekly scoped", 35, 65, "2026-07-25T18:00:00Z")

	encoded, marshalErr := json.Marshal(snapshots)
	if marshalErr != nil {
		t.Fatalf("marshal snapshots: %v", marshalErr)
	}
	for _, secret := range []string{
		"test-access-token-must-not-leak",
		"refresh-token-must-not-leak",
		"sk-response-token-must-not-leak",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("secret crossed snapshot boundary: %s", encoded)
		}
	}
}

func TestAdapter_UsesClaudeConfigDirIncludingCyrillicWindowsPath(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "Клод", "профиль")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	writeCredentialsAt(t, configDir, "token", time.Now().Add(time.Hour))

	server := usageServer(t, http.StatusOK, `{"five_hour":{"utilization":1}}`)
	defer server.Close()
	snapshots, err := NewAdapter(Config{Endpoint: server.URL}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusOK {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestAdapter_ReturnsUnavailableForMissingExpiredAuthAndUnauthorizedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(t *testing.T, configDir string) string
	}{
		{name: "missing credentials", setup: func(*testing.T, string) string { return "" }},
		{name: "expired credentials", setup: func(t *testing.T, configDir string) string {
			writeCredentialsAt(t, configDir, "expired-token", time.Now().Add(-time.Minute))
			return ""
		}},
		{name: "unauthorized response", setup: func(t *testing.T, configDir string) string {
			writeCredentialsAt(t, configDir, "token", time.Now().Add(time.Hour))
			server := usageServer(t, http.StatusUnauthorized, `{}`)
			t.Cleanup(server.Close)
			return server.URL
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			configDir := t.TempDir()
			endpoint := testCase.setup(t, configDir)
			if endpoint == "" {
				endpoint = "http://127.0.0.1:1/api/oauth/usage"
			}
			snapshots, err := NewAdapter(Config{ConfigDir: configDir, Endpoint: endpoint}).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusUnavailable || snapshots[0].ErrorNote != "usage_unavailable" {
				t.Fatalf("snapshots = %#v", snapshots)
			}
		})
	}
}

func TestAdapter_ReturnsStaleLastGoodSnapshotOnRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":20}}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	configDir := writeCredentials(t, "token", time.Now().Add(time.Hour))
	adapter := NewAdapter(Config{ConfigDir: configDir, Endpoint: server.URL})
	first, err := adapter.Collect(context.Background())
	if err != nil || len(first) != 1 || first[0].Status != providerlimits.StatusOK {
		t.Fatalf("first Collect() = %#v, %v", first, err)
	}
	stale, err := adapter.Collect(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limited error = %v, want ErrRateLimited", err)
	}
	if len(stale) != 1 || stale[0].Status != providerlimits.StatusStale || stale[0].ErrorNote != "rate_limited" {
		t.Fatalf("stale snapshots = %#v", stale)
	}
	for _, bucket := range stale[0].Buckets {
		if bucket.Status != providerlimits.StatusStale {
			t.Fatalf("stale bucket = %#v", bucket)
		}
	}
}

func TestAdapter_DoesNotExposeCredentialFromTransportError(t *testing.T) {
	const token = "test-access-token-must-not-cross-error-boundary"
	configDir := writeCredentials(t, token, time.Now().Add(time.Hour))
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Bearer " + token)
	})}

	snapshots, err := NewAdapter(Config{ConfigDir: configDir, Endpoint: "https://example.test/api/oauth/usage", Client: client}).Collect(context.Background())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("Collect() error = %v, must be generic", err)
	}
	encoded, marshalErr := json.Marshal(snapshots)
	if marshalErr != nil {
		t.Fatalf("marshal snapshots: %v", marshalErr)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatalf("credential crossed snapshot boundary: %s", encoded)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func usageServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func writeCredentials(t *testing.T, token string, expiresAt time.Time) string {
	t.Helper()
	configDir := t.TempDir()
	writeCredentialsAt(t, configDir, token, expiresAt)
	return configDir
}

func writeCredentialsAt(t *testing.T, configDir, token string, expiresAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	contents := `{"claudeAiOauth":{"accessToken":` + quoteJSON(token) + `,"refreshToken":"refresh-token-must-not-leak","expiresAt":` + quoteJSON(expiresAt.UTC().Format(time.RFC3339)) + `,"subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

func assertPercentBucket(t *testing.T, bucket providerlimits.Bucket, id, label string, used, remaining float64, reset string) {
	t.Helper()
	if bucket.ID != id || bucket.Label != label || bucket.Unit != providerlimits.UnitPercent || bucket.LimitValue == nil || *bucket.LimitValue != 100 || bucket.UsedValue == nil || *bucket.UsedValue != used || bucket.RemainingValue == nil || *bucket.RemainingValue != remaining || bucket.Status != providerlimits.StatusOK {
		t.Fatalf("bucket = %#v", bucket)
	}
	if reset == "" {
		if bucket.ResetsAt != nil {
			t.Fatalf("reset = %s, want nil", bucket.ResetsAt)
		}
		return
	}
	want, err := time.Parse(time.RFC3339, reset)
	if err != nil || bucket.ResetsAt == nil || !bucket.ResetsAt.Equal(want) {
		t.Fatalf("reset = %v, want %s", bucket.ResetsAt, reset)
	}
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

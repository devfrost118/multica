package codex

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

func TestAdapter_CollectsUsageAndIgnoresUnknownFields(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"account_id":"user-Q7nL2pX4mV8sK1dR5tY9cH3b",
			"email":"demo@gmail.com",
			"plan_type":"plus",
			"rate_limit":{
				"primary_window":{"used_percent":4,"limit_window_seconds":18000,"reset_at":1772962692,"unknown":true},
				"secondary_window":{"used_percent":1,"limit_window_seconds":604800,"reset_at":1773549492}
			},
			"future_field":"sk-response-token-must-not-leak"
		}`))
	}))
	defer server.Close()

	now := time.Date(2026, time.July, 19, 17, 0, 0, 0, time.UTC)
	home := writeAuth(t, "test-access-token-must-not-leak")
	snapshots, err := NewAdapter(Config{Home: home, Endpoint: server.URL + "/backend-api/wham/usage", Now: func() time.Time { return now }}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if authorization != "Bearer test-access-token-must-not-leak" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Provider != "codex" || !snapshot.CheckedAt.Equal(now) {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.AccountKey == "unavailable" || len(snapshot.AccountKey) != 16 {
		t.Fatalf("snapshot account key = %q, want a stable hash prefix", snapshot.AccountKey)
	}
	if snapshot.AccountLabel != "profile-plus" {
		t.Fatalf("snapshot account label = %q, want profile-plus", snapshot.AccountLabel)
	}
	if snapshot.Status != providerlimits.StatusOK || snapshot.Source.Kind != providerlimits.SourceKindOfficialAPI || snapshot.Source.Confidence != providerlimits.ConfidenceOfficial {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if len(snapshot.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %#v", len(snapshot.Buckets), snapshot.Buckets)
	}
	assertPercentBucket(t, snapshot.Buckets[0], "session", "Limit session", 4, 96, time.Unix(1772962692, 0).UTC())
	assertPercentBucket(t, snapshot.Buckets[1], "weekly", "Limit weekly", 1, 99, time.Unix(1773549492, 0).UTC())

	encoded, marshalErr := json.Marshal(snapshots)
	if marshalErr != nil {
		t.Fatalf("marshal snapshots: %v", marshalErr)
	}
	for _, secret := range []string{
		"test-access-token-must-not-leak",
		"sk-response-token-must-not-leak",
		"user-Q7nL2pX4mV8sK1dR5tY9cH3b",
		"demo@gmail.com",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("secret or raw identity crossed snapshot boundary: %s", encoded)
		}
	}
}

func TestAdapter_ProducesStableAccountKeyForSameIdentity(t *testing.T) {
	server := usageServer(t, http.StatusOK, `{"account_id":"acct-123","plan_type":"pro","rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1}}}`)
	defer server.Close()

	home := writeAuth(t, "token")
	adapter := NewAdapter(Config{Home: home, Endpoint: server.URL})
	first, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	second, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if first[0].AccountKey != second[0].AccountKey {
		t.Fatalf("account key changed across identical identity: %q vs %q", first[0].AccountKey, second[0].AccountKey)
	}
}

func TestAdapter_UsesCodexHomeAndHandlesCyrillicWindowsPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Кодекс", "профиль")
	t.Setenv("CODEX_HOME", home)
	writeAuthAt(t, home, "token")

	server := usageServer(t, http.StatusOK, `{"plan_type":"free","rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1}}}`)
	defer server.Close()

	snapshots, err := NewAdapter(Config{Endpoint: server.URL}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusOK || snapshots[0].AccountLabel != "profile-free" {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

// TestAdapter_ClassifiesPrimaryWindowByDurationNotPosition reproduces a real
// observed response shape: once an account is fully limited on its weekly
// quota, the endpoint reports that 7-day window as primary_window and omits
// secondary_window entirely. The bucket must still be labeled "Limit weekly",
// not "Limit session", because classification depends on limit_window_seconds
// rather than which JSON slot the window arrived in.
func TestAdapter_ClassifiesPrimaryWindowByDurationNotPosition(t *testing.T) {
	server := usageServer(t, http.StatusOK, `{"account_id":"acct-1","plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":313204,"reset_at":1784965393},"secondary_window":null}}`)
	defer server.Close()

	home := writeAuth(t, "token")
	snapshots, err := NewAdapter(Config{Home: home, Endpoint: server.URL}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if len(snapshots[0].Buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1: %#v", len(snapshots[0].Buckets), snapshots[0].Buckets)
	}
	assertPercentBucket(t, snapshots[0].Buckets[0], "weekly", "Limit weekly", 100, 0, time.Unix(1784965393, 0).UTC())
}

// TestAdapter_IgnoresWindowsThatDoNotMatchEitherDuration guards against a
// window whose limit_window_seconds falls outside both the session (<=6h)
// and weekly (>=6d) ranges (e.g. a 2-day promo window) silently masquerading
// as one or the other.
func TestAdapter_IgnoresWindowsThatDoNotMatchEitherDuration(t *testing.T) {
	server := usageServer(t, http.StatusOK, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":50,"limit_window_seconds":172800,"reset_at":1}}}`)
	defer server.Close()

	home := writeAuth(t, "token")
	snapshots, err := NewAdapter(Config{Home: home, Endpoint: server.URL}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != providerlimits.StatusUnavailable {
		t.Fatalf("snapshots = %#v, want unavailable since no window classified", snapshots)
	}
}

func TestAdapter_ReturnsUnavailableForMissingAuthAndUnauthorizedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(t *testing.T, home string) string
	}{
		{name: "missing auth", setup: func(*testing.T, string) string { return "" }},
		{name: "unauthorized response", setup: func(t *testing.T, home string) string {
			writeAuthAt(t, home, "token")
			server := usageServer(t, http.StatusUnauthorized, `{}`)
			t.Cleanup(server.Close)
			return server.URL
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			endpoint := testCase.setup(t, home)
			if endpoint == "" {
				endpoint = "http://127.0.0.1:1/backend-api/wham/usage"
			}
			snapshots, err := NewAdapter(Config{Home: home, Endpoint: endpoint}).Collect(context.Background())
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
			_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":18000,"reset_at":1}}}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	home := writeAuth(t, "token")
	adapter := NewAdapter(Config{Home: home, Endpoint: server.URL})
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
	home := writeAuth(t, token)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Bearer " + token)
	})}

	snapshots, err := NewAdapter(Config{Home: home, Endpoint: "https://example.test/backend-api/wham/usage", Client: client}).Collect(context.Background())
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

func writeAuth(t *testing.T, token string) string {
	t.Helper()
	home := t.TempDir()
	writeAuthAt(t, home, token)
	return home
}

func writeAuthAt(t *testing.T, home, token string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}
	contents := `{"tokens":{"access_token":` + quoteJSON(token) + `,"refresh_token":"refresh-token-must-not-leak","account_id":"acct-must-not-leak"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func assertPercentBucket(t *testing.T, bucket providerlimits.Bucket, id, label string, used, remaining float64, resetsAt time.Time) {
	t.Helper()
	if bucket.ID != id || bucket.Label != label || bucket.Unit != providerlimits.UnitPercent || bucket.LimitValue == nil || *bucket.LimitValue != 100 || bucket.UsedValue == nil || *bucket.UsedValue != used || bucket.RemainingValue == nil || *bucket.RemainingValue != remaining || bucket.ResetsAt == nil || !bucket.ResetsAt.Equal(resetsAt) || bucket.Status != providerlimits.StatusOK {
		t.Fatalf("bucket %s = %#v", id, bucket)
	}
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

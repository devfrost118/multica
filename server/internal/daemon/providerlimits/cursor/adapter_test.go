package cursor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
	_ "modernc.org/sqlite"
)

func TestAdapterReadsCursorSessionReadOnlyAndUsesOnlyUsageEndpoints(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	token := testJWT(t, "workos|user_123", now.Add(time.Hour))
	dbPath := writeStateDB(t, token)
	before := fileDigest(t, dbPath)
	transport := &recordingTransport{responses: map[string]string{
		"GET /api/auth/me": `{"email":"dev@example.com"}`,
		"POST /api/dashboard/get-current-period-usage": `{
			"billingCycleStart":"2026-07-01T00:00:00Z",
			"billingCycleEnd":"2026-08-01T00:00:00Z",
			"planUsage":{
				"autoPercentUsed":25,
				"apiPercentUsed":35,
				"totalPercentUsed":45,
				"totalSpend":1200,
				"includedSpend":800,
				"bonusSpend":200,
				"limit":2000
			}
		}`,
	}}

	snapshots, err := NewAdapter(Config{
		StateDBPath: dbPath,
		Client:      &http.Client{Transport: transport},
		Now:         func() time.Time { return now },
	}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	after := fileDigest(t, dbPath)
	if before != after {
		t.Fatal("state.vscdb changed during read-only collection")
	}
	if got := transport.requestKeys(); strings.Join(got, ",") != "GET /api/auth/me,POST /api/dashboard/get-current-period-usage" {
		t.Fatalf("requests = %v", got)
	}
	for _, request := range transport.requests {
		if request.URL.Scheme != "https" || request.URL.Host != "cursor.com" {
			t.Fatalf("request escaped cursor.com: %s", request.URL)
		}
		if strings.Contains(request.URL.Path, "login") || strings.Contains(request.URL.Path, "oauth") || strings.Contains(request.URL.Path, "refresh") {
			t.Fatalf("auth-flow request invoked: %s", request.URL.Path)
		}
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Provider != "cursor" || snapshot.AccountKey == "unavailable" || len(snapshot.AccountKey) != 16 || snapshot.AccountLabel == "dev@example.com" {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if len(snapshot.Buckets) != 4 {
		t.Fatalf("bucket count = %d: %#v", len(snapshot.Buckets), snapshot.Buckets)
	}
	assertPercent(t, snapshot.Buckets[0], "auto", 25)
	assertPercent(t, snapshot.Buckets[1], "api", 35)
	assertPercent(t, snapshot.Buckets[2], "total", 45)
	spend := snapshot.Buckets[3]
	if spend.ID != "spend" || spend.Unit != providerlimits.UnitCurrency || spend.UsedValue == nil || *spend.UsedValue != 12 || spend.LimitValue == nil || *spend.LimitValue != 20 {
		t.Fatalf("spend bucket = %#v", spend)
	}
	if !strings.Contains(spend.Note, "included_800") || !strings.Contains(spend.Note, "bonus_200") {
		t.Fatalf("spend pools lost: %q", spend.Note)
	}

	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), "user_123") || strings.Contains(string(encoded), "dev@example.com") {
		t.Fatalf("session or identity leaked: %s", encoded)
	}
}

func TestAdapterMissingOrExpiredSessionRequiresReauthWithoutNetwork(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name string
		path func(t *testing.T) string
	}{
		{name: "missing", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.vscdb") }},
		{name: "expired", path: func(t *testing.T) string { return writeStateDB(t, testJWT(t, "user_123", now.Add(-time.Minute))) }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &recordingTransport{responses: map[string]string{}}
			snapshots, err := NewAdapter(Config{
				StateDBPath: testCase.path(t),
				Client:      &http.Client{Transport: transport},
				Now:         func() time.Time { return now },
			}).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(transport.requests) != 0 {
				t.Fatalf("network invoked for unavailable session: %v", transport.requestKeys())
			}
			if snapshots[0].Status != providerlimits.StatusUnavailable || snapshots[0].ErrorNote != "reauth_required" {
				t.Fatalf("snapshot = %#v", snapshots[0])
			}
		})
	}
}

func TestAdapterUsesSchemaTolerantUsageSummaryFallback(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	transport := &recordingTransport{responses: map[string]string{
		"GET /api/auth/me": `{"email":"dev@example.com"}`,
		"POST /api/dashboard/get-current-period-usage": `{"unexpected":true}`,
		"GET /api/usage-summary":                       `{"billingCycleEnd":"2026-08-01T00:00:00Z","planUsage":{"totalPercentUsed":50}}`,
	}}
	snapshots, err := NewAdapter(Config{
		StateDBPath: writeStateDB(t, testJWT(t, "user_123", now.Add(time.Hour))),
		Client:      &http.Client{Transport: transport},
		Now:         func() time.Time { return now },
	}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := transport.requestKeys(); len(got) != 3 || got[2] != "GET /api/usage-summary" {
		t.Fatalf("requests = %v", got)
	}
	if len(snapshots[0].Buckets) != 1 || snapshots[0].Buckets[0].ID != "total" {
		t.Fatalf("fallback snapshot = %#v", snapshots[0])
	}
}

type recordingTransport struct {
	mu        sync.Mutex
	requests  []*http.Request
	responses map[string]string
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	cloned := request.Clone(request.Context())
	t.requests = append(t.requests, cloned)
	t.mu.Unlock()
	key := request.Method + " " + request.URL.Path
	body, ok := t.responses[key]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
		body = `{}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func (t *recordingTransport) requestKeys() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := make([]string, len(t.requests))
	for index, request := range t.requests {
		keys[index] = request.Method + " " + request.URL.Path
	}
	return keys
}

func writeStateDB(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture sqlite: %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create ItemTable: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, "cursorAuth/accessToken", token); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture sqlite: %v", err)
	}
	return path
}

func testJWT(t *testing.T, sub string, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"sub": sub, "exp": expiresAt.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(contents)
}

func assertPercent(t *testing.T, bucket providerlimits.Bucket, id string, used float64) {
	t.Helper()
	if bucket.ID != id || bucket.Unit != providerlimits.UnitPercent || bucket.UsedValue == nil || *bucket.UsedValue != used {
		t.Fatalf("bucket %s = %#v", id, bucket)
	}
}

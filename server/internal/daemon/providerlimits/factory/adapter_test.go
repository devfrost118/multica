package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

func TestAdapterCollectsStandardCoreAndExtraUsageWithOneRequest(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/billing/limits" || r.URL.RawQuery != "" {
			t.Fatalf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer factory-test-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{
			"usesTokenRateLimitsBilling": true,
			"limits": {
				"standard": {
					"fiveHour": {"usedPercent": 22, "windowEnd": "2026-07-23T17:00:00Z", "secondsRemaining": 18000},
					"weekly": {"usedPercent": 33, "windowEnd": "2026-07-30T12:00:00Z", "secondsRemaining": 604800},
					"monthly": {"usedPercent": 44, "windowEnd": "2026-08-23T12:00:00Z", "secondsRemaining": 2678400}
				},
				"core": {
					"fiveHour": {"usedPercent": 55, "windowEnd": "2026-07-23T17:00:00Z", "secondsRemaining": 18000},
					"weekly": {"usedPercent": 66, "windowEnd": "2026-07-30T12:00:00Z", "secondsRemaining": 604800},
					"monthly": {"usedPercent": 77, "windowEnd": "2026-08-23T12:00:00Z", "secondsRemaining": 2678400}
				}
			},
			"extraUsageBalanceCents": 1234,
			"extraUsageAllowed": true,
			"overagePreference": "enabled",
			"futureSecret": "must-not-leak"
		}`))
	}))
	defer server.Close()

	adapter := NewAdapter(Config{
		Endpoint: server.URL + "/api/billing/limits",
		Now:      func() time.Time { return now },
		Credentials: []Credential{{
			ID:           "11111111-1111-1111-1111-111111111111",
			Token:        "factory-test-token",
			AccountLabel: "Factory work",
		}},
	})
	snapshots, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("request count = %d, want 1", calls.Load())
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Provider != "factory" || snapshot.AccountKey == "unavailable" || len(snapshot.AccountKey) != 16 {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if snapshot.AccountLabel != "Factory work" || snapshot.Status != providerlimits.StatusOK {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if len(snapshot.Buckets) != 7 {
		t.Fatalf("bucket count = %d, want 7: %#v", len(snapshot.Buckets), snapshot.Buckets)
	}
	assertPercentBucket(t, snapshot.Buckets[0], "standard_5h", 22, 78, true)
	assertPercentBucket(t, snapshot.Buckets[3], "core_5h", 55, 45, true)
	extra := snapshot.Buckets[6]
	if extra.ID != "extra_usage" || extra.Unit != providerlimits.UnitCurrency || extra.RemainingValue == nil || *extra.RemainingValue != 12.34 {
		t.Fatalf("extra usage bucket = %#v", extra)
	}
	if !strings.Contains(extra.Note, "allowed") || !strings.Contains(extra.Note, "enabled") {
		t.Fatalf("extra usage note = %q", extra.Note)
	}

	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"factory-test-token", "must-not-leak"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("secret crossed adapter boundary: %s", encoded)
		}
	}
}

func TestAdapterNormalizesInactiveFiveHourWindow(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"usesTokenRateLimitsBilling": true,
			"limits": {"standard": {"fiveHour": {
				"usedPercent": 91,
				"windowEnd": "2026-07-23T11:00:00Z",
				"secondsRemaining": null
			}}}
		}`))
	}))
	defer server.Close()

	snapshots, err := NewAdapter(Config{
		Endpoint:    server.URL,
		Now:         func() time.Time { return now },
		Credentials: []Credential{{ID: "credential-1", Token: "token"}},
	}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	bucket := snapshots[0].Buckets[0]
	if bucket.UsedValue == nil || *bucket.UsedValue != 0 || bucket.RemainingValue == nil || *bucket.RemainingValue != 100 || bucket.ResetsAt != nil {
		t.Fatalf("inactive bucket = %#v", bucket)
	}
}

func TestAdapterRetainsLastGoodAndSanitizesFailures(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			_, _ = w.Write([]byte(`{"usesTokenRateLimitsBilling":true,"limits":{"standard":{"weekly":{"usedPercent":10,"windowEnd":"2026-07-30T12:00:00Z","secondsRemaining":1}}}}`))
		}
	}))
	defer server.Close()

	adapter := NewAdapter(Config{Endpoint: server.URL, Credentials: []Credential{{ID: "credential-1", Token: "token"}}})
	first, err := adapter.Collect(context.Background())
	if err != nil || first[0].Status != providerlimits.StatusOK {
		t.Fatalf("first Collect() = %#v, %v", first, err)
	}

	status.Store(http.StatusTooManyRequests)
	stale, err := adapter.Collect(context.Background())
	if err != ErrRateLimited {
		t.Fatalf("429 error = %v, want ErrRateLimited", err)
	}
	if stale[0].Status != providerlimits.StatusStale || stale[0].ErrorNote != "rate_limited" || len(stale[0].Buckets) != 1 {
		t.Fatalf("stale snapshot = %#v", stale[0])
	}

	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		status.Store(int32(code))
		unavailable, collectErr := adapter.Collect(context.Background())
		if collectErr != nil || unavailable[0].Status != providerlimits.StatusUnavailable || unavailable[0].ErrorNote != "credential_invalid" {
			t.Fatalf("%d snapshot = %#v, err = %v", code, unavailable, collectErr)
		}
	}
}

func TestAdapterAccountKeyIsStableAcrossTokenReplacement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"usesTokenRateLimitsBilling":true,"limits":{"standard":{"weekly":{"usedPercent":1,"windowEnd":"2026-07-30T12:00:00Z","secondsRemaining":1}}}}`))
	}))
	defer server.Close()

	adapter := NewAdapter(Config{Endpoint: server.URL, Credentials: []Credential{{ID: "stable-row-id", Token: "first"}}})
	first, _ := adapter.Collect(context.Background())
	adapter.ReplaceCredentials([]Credential{{ID: "stable-row-id", Token: "second"}})
	second, _ := adapter.Collect(context.Background())
	if first[0].AccountKey != second[0].AccountKey {
		t.Fatalf("account key changed after token replacement: %q != %q", first[0].AccountKey, second[0].AccountKey)
	}
}

func assertPercentBucket(t *testing.T, bucket providerlimits.Bucket, id string, used, remaining float64, hasReset bool) {
	t.Helper()
	if bucket.ID != id || bucket.Unit != providerlimits.UnitPercent || bucket.UsedValue == nil || *bucket.UsedValue != used || bucket.RemainingValue == nil || *bucket.RemainingValue != remaining {
		t.Fatalf("bucket %s = %#v", id, bucket)
	}
	if hasReset && bucket.ResetsAt == nil {
		t.Fatalf("bucket %s has no reset", id)
	}
}

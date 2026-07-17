package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

func TestProviderLimitsReporterContinuesAfterRuntimeFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	seen := make(map[string]bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = true
		mu.Unlock()
		if r.URL.Path == "/api/daemon/runtimes/runtime-1/provider-limits" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	reporter := providerLimitsReporter{
		client:     NewClient(srv.URL),
		runtimeIDs: func() []string { return []string{"runtime-1", "runtime-2"} },
	}
	err := reporter.Report(context.Background(), []providerlimits.AccountSnapshot{testProviderLimitSnapshot()})
	if err == nil {
		t.Fatal("Report() error = nil, want aggregate runtime failure")
	}
	var requestErr *requestError
	if !errors.As(err, &requestErr) {
		t.Fatal("aggregate error must remain inspectable")
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen["/api/daemon/runtimes/runtime-2/provider-limits"] {
		t.Fatalf("second runtime did not receive report: %#v", seen)
	}
}

func testProviderLimitSnapshot() providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:   "claude",
		AccountKey: "a1b2c3d4",
		Status:     providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:       providerlimits.SourceKindLocalAuthState,
			Confidence: providerlimits.ConfidenceOfficial,
		},
	}
}

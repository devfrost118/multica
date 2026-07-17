package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

func TestClient_ReportProviderLimitsPostsNormalizedBatchForRuntime(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/daemon/runtimes/runtime-1/provider-limits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct {
			Snapshots []providerlimits.AccountSnapshot `json:"snapshots"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.Snapshots) != 1 || body.Snapshots[0].Provider != "claude" {
			t.Fatalf("snapshots = %#v", body.Snapshots)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	snapshot := providerlimits.AccountSnapshot{
		Provider:   "claude",
		AccountKey: "a1b2c3d4",
		CheckedAt:  time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
		Status:     providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:       providerlimits.SourceKindLocalAuthState,
			Confidence: providerlimits.ConfidenceOfficial,
		},
	}

	if err := client.ReportProviderLimits(context.Background(), "runtime-1", []providerlimits.AccountSnapshot{snapshot}); err != nil {
		t.Fatalf("ReportProviderLimits() error = %v", err)
	}
}

func TestClient_ReportProviderLimitsSanitizesBeforeWritingRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		if bytes.Contains(body.Bytes(), []byte("sk-secret-token")) || bytes.Contains(body.Bytes(), []byte(".credentials.json")) {
			t.Fatalf("unsafe provider data crossed transport boundary: %s", body.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	snapshot := providerlimits.AccountSnapshot{
		Provider:   "claude",
		AccountKey: "a1b2c3d4",
		CheckedAt:  time.Now().UTC(),
		Status:     providerlimits.StatusPartial,
		Source: providerlimits.Source{
			Kind:       providerlimits.SourceKindLocalAuthState,
			Confidence: providerlimits.ConfidenceOfficial,
		},
		ErrorNote: `raw C:\\Users\\Ada\\.claude\\.credentials.json sk-secret-token`,
	}

	if err := client.ReportProviderLimits(context.Background(), "runtime-1", []providerlimits.AccountSnapshot{snapshot}); err != nil {
		t.Fatalf("ReportProviderLimits() error = %v", err)
	}
}

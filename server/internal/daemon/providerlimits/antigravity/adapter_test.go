package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

const (
	testAccessToken = "ya29.test-access-token-value"
	testProject     = "projects/multica-antigravity-test"
	testEmail       = "dev@example.com"
	testAuthPath    = `C:\Users\dev\.gemini\credentials.json`
	testResetTime   = "2026-07-27T21:48:31Z"
)

func TestAdapterReportsOneSessionBucketFromAccountQuota(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	service, baseURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
		fetchAvailableModelsPath: {status: http.StatusOK, body: modelsFixture(1)},
	})

	snapshots, err := newTestAdapter(baseURL, now, validCredential(now)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.Provider != provider || snapshot.Status != providerlimits.StatusOK {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.AccountKey != accountKeyFrom(testProject) || len(snapshot.AccountKey) != 16 {
		t.Fatalf("account key = %q", snapshot.AccountKey)
	}
	if snapshot.AccountLabel != "profile-gemini-code-assist-in-google-one-ai-pro" {
		t.Fatalf("account label = %q", snapshot.AccountLabel)
	}
	if len(snapshot.Buckets) != 1 {
		t.Fatalf("bucket count = %d: %#v", len(snapshot.Buckets), snapshot.Buckets)
	}
	bucket := snapshot.Buckets[0]
	if bucket.ID != sessionBucketID || bucket.Label != sessionBucketLabel || bucket.Unit != providerlimits.UnitPercent {
		t.Fatalf("bucket identity = %#v", bucket)
	}
	// Internal models sit at remainingFraction 0.1; counting them would report 90.
	if bucket.UsedValue == nil || *bucket.UsedValue != 0 || bucket.RemainingValue == nil || *bucket.RemainingValue != 100 {
		t.Fatalf("bucket values = %#v", bucket)
	}
	if bucket.ResetsAt != nil {
		t.Fatalf("untouched quota exposed a sliding reset: %v", bucket.ResetsAt)
	}

	requests := service.recorded()
	if len(requests) != 2 || requests[0].path != loadCodeAssistPath || requests[1].path != fetchAvailableModelsPath {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].body != `{"metadata":{"ideType":"ANTIGRAVITY"}}` {
		t.Fatalf("loadCodeAssist body = %s", requests[0].body)
	}
	if requests[1].body != fmt.Sprintf(`{"project":%q}`, testProject) {
		t.Fatalf("fetchAvailableModels body = %s", requests[1].body)
	}
	for _, request := range requests {
		if !strings.Contains(strings.ToLower(request.userAgent), "antigravity") {
			t.Fatalf("user agent lost the antigravity gate: %q", request.userAgent)
		}
		if request.googUserProject != "" {
			t.Fatalf("x-goog-user-project sent: %q", request.googUserProject)
		}
		if request.authorization != "Bearer "+testAccessToken {
			t.Fatalf("authorization header = %q", request.authorization)
		}
	}
}

func TestAdapterReportsUsedPercentAndEarliestResetWhenQuotaConsumed(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	_, baseURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
		fetchAvailableModelsPath: {status: http.StatusOK, body: modelsFixture(0.42)},
	})

	snapshots, err := newTestAdapter(baseURL, now, validCredential(now)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	bucket := snapshots[0].Buckets[0]
	if bucket.UsedValue == nil || *bucket.UsedValue != 58 || bucket.RemainingValue == nil || *bucket.RemainingValue != 42 {
		t.Fatalf("bucket values = %#v", bucket)
	}
	if bucket.ResetsAt == nil || !bucket.ResetsAt.Equal(time.Date(2026, time.July, 27, 21, 48, 31, 0, time.UTC)) {
		t.Fatalf("resets at = %v", bucket.ResetsAt)
	}
}

func TestAdapterRequiresReauthWithoutNetworkWhenKeyringSessionUnusable(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name       string
		credential func() ([]byte, error)
		reason     string
	}{
		{name: "keyring_empty", credential: func() ([]byte, error) { return nil, errAuthUnavailable }, reason: "reauth_required"},
		{name: "blob_not_json", credential: staticCredential([]byte("not json")), reason: "reauth_required"},
		{name: "token_missing", credential: staticCredential([]byte(`{"token":{"expiry":"2026-07-27T21:00:00Z"}}`)), reason: "reauth_required"},
		{name: "token_expired", credential: staticCredential(credentialBlob(testAccessToken, now.Add(-time.Minute))), reason: "reauth_required"},
		{name: "token_expires_in_flight", credential: staticCredential(credentialBlob(testAccessToken, now.Add(30*time.Second))), reason: "reauth_required"},
		{name: "platform_unsupported", credential: func() ([]byte, error) { return nil, errUnsupportedPlatform }, reason: "unsupported_platform"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, baseURL := newFakeService(t, map[string]route{})
			snapshots, err := newTestAdapter(baseURL, now, testCase.credential).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(service.recorded()) != 0 {
				t.Fatalf("network invoked without a usable session: %#v", service.recorded())
			}
			if snapshots[0].Status != providerlimits.StatusUnavailable || snapshots[0].ErrorNote != testCase.reason {
				t.Fatalf("snapshot = %#v", snapshots[0])
			}
		})
	}
}

func TestAdapterMapsEndpointFailuresToReasons(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		routes    map[string]route
		reason    string
		wantError error
	}{
		{
			name: "unauthorized",
			routes: map[string]route{
				loadCodeAssistPath: {status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED"}}`},
			},
			reason: "reauth_required",
		},
		{
			name: "forbidden",
			routes: map[string]route{
				loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
				fetchAvailableModelsPath: {status: http.StatusForbidden, body: `{"error":{"status":"PERMISSION_DENIED"}}`},
			},
			reason: "reauth_required",
		},
		{
			name: "malformed_json",
			routes: map[string]route{
				loadCodeAssistPath: {status: http.StatusOK, body: `{"cloudaicompanionProject":`},
			},
			reason:    "usage_unavailable",
			wantError: errUsageUnavailable,
		},
		{
			name: "models_schema_drift",
			routes: map[string]route{
				loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
				fetchAvailableModelsPath: {status: http.StatusOK, body: `{"models":[{"quotaInfo":{"remainingFraction":0.5}}]}`},
			},
			reason:    "usage_unavailable",
			wantError: errUsageUnavailable,
		},
		{
			name: "models_empty",
			routes: map[string]route{
				loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
				fetchAvailableModelsPath: {status: http.StatusOK, body: `{"models":{}}`},
			},
			reason: "usage_unavailable",
		},
		{
			name: "models_without_quota",
			routes: map[string]route{
				loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
				fetchAvailableModelsPath: {status: http.StatusOK, body: `{"models":{"tab_edit":{"quotaInfo":{"remainingFraction":1}}}}`},
			},
			reason: "usage_unavailable",
		},
		{
			name: "project_missing",
			routes: map[string]route{
				loadCodeAssistPath: {status: http.StatusOK, body: `{"currentTier":{"name":"Gemini Code Assist"}}`},
			},
			reason: "onboarding_required",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, baseURL := newFakeService(t, testCase.routes)
			snapshots, err := newTestAdapter(baseURL, now, validCredential(now)).Collect(context.Background())
			if testCase.wantError == nil && err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if testCase.wantError != nil && !errors.Is(err, testCase.wantError) {
				t.Fatalf("Collect() error = %v, want %v", err, testCase.wantError)
			}
			if snapshots[0].Status != providerlimits.StatusUnavailable || snapshots[0].ErrorNote != testCase.reason {
				t.Fatalf("snapshot = %#v", snapshots[0])
			}
		})
	}
}

func TestAdapterKeepsLastGoodSnapshotWhenRateLimited(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	service, baseURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
		fetchAvailableModelsPath: {status: http.StatusOK, body: modelsFixture(0.42)},
	})
	adapter := newTestAdapter(baseURL, now, validCredential(now))
	if _, err := adapter.Collect(context.Background()); err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}

	service.setRoute(fetchAvailableModelsPath, route{status: http.StatusTooManyRequests, body: `{"error":{"status":"RESOURCE_EXHAUSTED"}}`})
	snapshots, err := adapter.Collect(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Collect() error = %v, want ErrRateLimited", err)
	}
	snapshot := snapshots[0]
	if snapshot.Status != providerlimits.StatusStale || snapshot.ErrorNote != "rate_limited" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.Buckets) != 1 || snapshot.Buckets[0].Status != providerlimits.StatusStale {
		t.Fatalf("buckets = %#v", snapshot.Buckets)
	}
	if snapshot.Buckets[0].UsedValue == nil || *snapshot.Buckets[0].UsedValue != 58 {
		t.Fatalf("last-good reading lost: %#v", snapshot.Buckets[0])
	}
}

func TestAdapterFallsBackToSecondaryHostOnServerFailure(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	primary, primaryURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusInternalServerError, body: `{"error":{"status":"INTERNAL"}}`},
		fetchAvailableModelsPath: {status: http.StatusInternalServerError, body: `{"error":{"status":"INTERNAL"}}`},
	})
	secondary, secondaryURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
		fetchAvailableModelsPath: {status: http.StatusOK, body: modelsFixture(0.42)},
	})

	adapter := NewAdapter(Config{
		BaseURLs:         []string{primaryURL, secondaryURL},
		Client:           &http.Client{},
		Now:              func() time.Time { return now },
		CredentialReader: validCredential(now),
	})
	snapshots, err := adapter.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if snapshots[0].Status != providerlimits.StatusOK {
		t.Fatalf("snapshot = %#v", snapshots[0])
	}
	// The failing host is tried once, then the known-good host serves both calls.
	if len(primary.recorded()) != 1 || len(secondary.recorded()) != 2 {
		t.Fatalf("primary requests = %d, secondary requests = %d", len(primary.recorded()), len(secondary.recorded()))
	}
}

func TestSanitizedSnapshotCarriesNoTokenEmailOrLocalPath(t *testing.T) {
	now := time.Date(2026, time.July, 27, 17, 0, 0, 0, time.UTC)
	_, baseURL := newFakeService(t, map[string]route{
		loadCodeAssistPath:       {status: http.StatusOK, body: loadCodeAssistFixture(testProject)},
		fetchAvailableModelsPath: {status: http.StatusOK, body: modelsFixture(0.42)},
	})

	snapshots, err := newTestAdapter(baseURL, now, validCredential(now)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	encoded, err := json.Marshal(providerlimits.SanitizeSnapshots(snapshots, providerlimits.SanitizationCaps{}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, secret := range []string{testAccessToken, testEmail, testProject, testAuthPath, "refresh-secret", keyringTarget} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive value %q survived sanitization: %s", secret, encoded)
		}
	}
	var sanitized []providerlimits.AccountSnapshot
	if err := json.Unmarshal(encoded, &sanitized); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(sanitized) != 1 || len(sanitized[0].Buckets) != 1 || sanitized[0].Buckets[0].ID != sessionBucketID {
		t.Fatalf("sanitization dropped the session bucket: %#v", sanitized)
	}
	if sanitized[0].AccountLabel != "profile-gemini-code-assist-in-google-one-ai-pro" {
		t.Fatalf("sanitization dropped the plan label: %q", sanitized[0].AccountLabel)
	}
}

func newTestAdapter(baseURL string, now time.Time, credential func() ([]byte, error)) *Adapter {
	return NewAdapter(Config{
		BaseURLs:         []string{baseURL},
		Client:           &http.Client{},
		Now:              func() time.Time { return now },
		CredentialReader: credential,
	})
}

func validCredential(now time.Time) func() ([]byte, error) {
	return staticCredential(credentialBlob(testAccessToken, now.Add(time.Hour)))
}

func staticCredential(blob []byte) func() ([]byte, error) {
	return func() ([]byte, error) { return blob, nil }
}

func credentialBlob(accessToken string, expiry time.Time) []byte {
	blob, err := json.Marshal(map[string]any{
		"token": map[string]any{
			"access_token":  accessToken,
			"token_type":    "Bearer",
			"refresh_token": "1//refresh-secret",
			"expiry":        expiry.Format(time.RFC3339Nano),
		},
		"auth_method": "consumer",
	})
	if err != nil {
		panic(err)
	}
	return blob
}

func loadCodeAssistFixture(project string) string {
	body, err := json.Marshal(map[string]any{
		"cloudaicompanionProject": project,
		"currentTier":             map[string]any{"id": "standard-tier", "name": "Gemini Code Assist"},
		"paidTier":                map[string]any{"id": "g1-pro-tier", "name": "Gemini Code Assist in Google One AI Pro"},
		"manageSubscriptionUri":   "https://payments.example.com/subscriptions?Email=" + testEmail,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// modelsFixture mirrors the observed shape: non-internal models sharing one
// fraction and resetTime, two completion models without a reset window, and two
// internal models that must never reach the bucket.
func modelsFixture(remainingFraction float64) string {
	models := make(map[string]any, 24)
	for index := 0; index < 20; index++ {
		models[fmt.Sprintf("gemini-3.6-model-%d", index)] = map[string]any{
			"displayName": "Gemini 3.6 (Thinking) " + testAuthPath,
			"model":       "MODEL_PLACEHOLDER_M26",
			"quotaInfo":   map[string]any{"remainingFraction": remainingFraction, "resetTime": testResetTime},
		}
	}
	for _, id := range []string{"tab_completion", "tab_edit"} {
		models[id] = map[string]any{"quotaInfo": map[string]any{"remainingFraction": 1}}
	}
	for _, id := range []string{"chat_internal_fast", "chat_internal_slow"} {
		models[id] = map[string]any{
			"isInternal": true,
			"quotaInfo":  map[string]any{"remainingFraction": 0.1, "resetTime": testResetTime},
		}
	}
	body, err := json.Marshal(map[string]any{
		"models":              models,
		"defaultAgentModelId": "gemini-3.6-flash-high",
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

type route struct {
	status int
	body   string
}

type recordedRequest struct {
	path            string
	body            string
	userAgent       string
	authorization   string
	googUserProject string
}

type fakeService struct {
	mu       sync.Mutex
	routes   map[string]route
	requests []recordedRequest
}

func newFakeService(t *testing.T, routes map[string]route) (*fakeService, string) {
	t.Helper()
	service := &fakeService{routes: make(map[string]route, len(routes))}
	for path, entry := range routes {
		service.routes[path] = entry
	}
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	return service, server.URL
}

func (s *fakeService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(request.Body, maxResponseBytes))
	s.mu.Lock()
	s.requests = append(s.requests, recordedRequest{
		path:            request.URL.Path,
		body:            string(body),
		userAgent:       request.Header.Get("User-Agent"),
		authorization:   request.Header.Get("Authorization"),
		googUserProject: request.Header.Get("x-goog-user-project"),
	})
	entry, ok := s.routes[request.URL.Path]
	s.mu.Unlock()
	if !ok {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(entry.status)
	_, _ = io.WriteString(writer, entry.body)
}

func (s *fakeService) setRoute(path string, entry route) {
	s.mu.Lock()
	s.routes[path] = entry
	s.mu.Unlock()
}

func (s *fakeService) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

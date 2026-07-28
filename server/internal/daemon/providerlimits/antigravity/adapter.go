// Package antigravity reads the Antigravity CLI (`agy`) OAuth session from the
// local OS keyring in read-only mode and normalizes Google Cloud Code Assist
// account quota into a single session bucket.
//
// It deliberately contains no login, token refresh, credential rotation, or
// account-mutating call: the keyring access token is used only while `agy`
// itself keeps it valid, and an expired token is reported as reauth_required.
// onboardUser is never called because it creates a Cloud Code project, and a
// limits widget must not change provider account state.
//
// Credentials, project identifiers, and raw endpoint payloads never leave this
// package. The MVP supports the Windows Credential Manager only; macOS and
// Linux report unsupported_platform.
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

const (
	provider         = "antigravity"
	defaultFreshness = 15 * time.Minute
	keyringTarget    = "gemini:antigravity"
	ideType          = "ANTIGRAVITY"

	loadCodeAssistPath       = "/v1internal:loadCodeAssist"
	fetchAvailableModelsPath = "/v1internal:fetchAvailableModels"

	// userAgent must contain "antigravity" (case-insensitive): Cloud Code
	// Assist answers fetchAvailableModels with 403 PERMISSION_DENIED for any
	// other client string. The gate is a substring check, not the exact `agy`
	// version string, so Multica identifies itself honestly instead of
	// impersonating the CLI.
	userAgent = "Multica/1.0 (antigravity-limits)"

	// tokenExpiryGuard skips a request whose access token would expire while it
	// is still in flight.
	tokenExpiryGuard = time.Minute

	maxResponseBytes = 1 << 20
)

// defaultBaseURLs is ordered by what the shipped CLI actually calls; the second
// host is the fallback for a host-level outage.
var defaultBaseURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com",
	"https://cloudcode-pa.googleapis.com",
}

// ErrRateLimited carries no provider response detail. Returning it alongside a
// stale snapshot lets the collector keep the last useful reading while applying
// its ordinary provider backoff.
var ErrRateLimited = errors.New("antigravity usage rate limited")

var (
	errAuthUnavailable     = errors.New("antigravity session unavailable")
	errUnsupportedPlatform = errors.New("antigravity keyring unsupported on this platform")
	errUsageUnavailable    = errors.New("antigravity usage unavailable")
	errReauthRequired      = errors.New("antigravity session rejected")
	errHostUnavailable     = errors.New("antigravity host unavailable")
)

// Config supplies testable HTTP and keyring dependencies. CredentialReader is
// the only seam tests need: the real OS keyring is never touched by them.
type Config struct {
	BaseURLs         []string
	Client           *http.Client
	Now              func() time.Time
	CredentialReader func() ([]byte, error)
}

// Adapter is a daemon-local Antigravity quota adapter.
type Adapter struct {
	baseURLs         []string
	client           *http.Client
	now              func() time.Time
	credentialReader func() ([]byte, error)

	mu           sync.Mutex
	lastGood     *providerlimits.AccountSnapshot
	knownBaseURL string
}

// NewAdapter constructs an adapter without touching local auth state.
func NewAdapter(config Config) *Adapter {
	baseURLs := make([]string, 0, len(config.BaseURLs))
	for _, baseURL := range config.BaseURLs {
		if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
			baseURLs = append(baseURLs, trimmed)
		}
	}
	if len(baseURLs) == 0 {
		baseURLs = append(baseURLs, defaultBaseURLs...)
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	reader := config.CredentialReader
	if reader == nil {
		reader = readCredentialBlob
	}
	return &Adapter{baseURLs: baseURLs, client: client, now: now, credentialReader: reader}
}

func (*Adapter) Provider() string { return provider }

func (*Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 20 * time.Second, MinimumInterval: defaultFreshness}
}

func (a *Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	checkedAt := a.now().UTC()

	token, err := a.accessToken(checkedAt)
	if errors.Is(err, errUnsupportedPlatform) {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "unsupported_platform")}, nil
	}
	if err != nil {
		return a.staleOrUnavailable(checkedAt, "reauth_required"), nil
	}

	var assist codeAssistResponse
	if err := a.post(ctx, token, loadCodeAssistPath, loadCodeAssistRequest{Metadata: requestMetadata{IDEType: ideType}}, &assist); err != nil {
		return a.snapshotsForRequestError(checkedAt, err)
	}
	project := strings.TrimSpace(assist.CloudaicompanionProject)
	if project == "" {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "onboarding_required")}, nil
	}

	var models availableModelsResponse
	if err := a.post(ctx, token, fetchAvailableModelsPath, fetchAvailableModelsRequest{Project: project}, &models); err != nil {
		return a.snapshotsForRequestError(checkedAt, err)
	}
	buckets, ok := sessionBuckets(models.Models)
	if !ok {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, "usage_unavailable")}, nil
	}

	snapshot := providerlimits.AccountSnapshot{
		Provider:     provider,
		AccountKey:   accountKeyFrom(project),
		AccountLabel: providerlimits.NormalizeProfileLabel(planName(assist)),
		CheckedAt:    checkedAt,
		Status:       providerlimits.StatusOK,
		Source:       snapshotSource(),
		Buckets:      buckets,
	}
	a.storeLastGood(snapshot)
	return []providerlimits.AccountSnapshot{snapshot}, nil
}

// accessToken reads the keyring blob and returns the bearer token only while it
// is still valid. It never touches the refresh token.
func (a *Adapter) accessToken(now time.Time) (string, error) {
	blob, err := a.credentialReader()
	if err != nil {
		return "", err
	}
	var credential keyringCredential
	if err := json.Unmarshal(blob, &credential); err != nil {
		return "", errAuthUnavailable
	}
	token := strings.TrimSpace(credential.Token.AccessToken)
	if token == "" {
		return "", errAuthUnavailable
	}
	expiry, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(credential.Token.Expiry))
	if err != nil || !expiry.After(now.Add(tokenExpiryGuard)) {
		return "", errAuthUnavailable
	}
	return token, nil
}

// post sends one Cloud Code Assist call, walking the base-URL candidates only
// while the failure is host-level. A 401/403/429 is terminal for this cycle:
// retrying it on the second host would only double the load.
func (a *Adapter) post(ctx context.Context, token, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errUsageUnavailable
	}
	for _, baseURL := range a.candidateBaseURLs() {
		err := a.postOnce(ctx, baseURL, token, path, body, out)
		if err == nil {
			a.rememberBaseURL(baseURL)
			return nil
		}
		if !errors.Is(err, errHostUnavailable) {
			return err
		}
	}
	return errUsageUnavailable
}

func (a *Adapter) postOnce(ctx context.Context, baseURL, token, path string, body []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return errUsageUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	// x-goog-user-project is deliberately absent: it switches billing to the
	// caller's own GCP project and turns this read into a 403.
	request.Header.Set("User-Agent", userAgent)

	response, err := a.client.Do(request)
	if err != nil {
		return errHostUnavailable
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusUnauthorized, response.StatusCode == http.StatusForbidden:
		return errReauthRequired
	case response.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case response.StatusCode == http.StatusNotFound, response.StatusCode >= http.StatusInternalServerError:
		return errHostUnavailable
	case response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices:
		return errUsageUnavailable
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(out); err != nil {
		return errUsageUnavailable
	}
	return nil
}

func (a *Adapter) snapshotsForRequestError(checkedAt time.Time, err error) ([]providerlimits.AccountSnapshot, error) {
	switch {
	case errors.Is(err, errReauthRequired):
		return a.staleOrUnavailable(checkedAt, "reauth_required"), nil
	case errors.Is(err, ErrRateLimited):
		return a.staleOrUnavailable(checkedAt, "rate_limited"), ErrRateLimited
	default:
		return a.staleOrUnavailable(checkedAt, "usage_unavailable"), errUsageUnavailable
	}
}

// candidateBaseURLs puts the last known-good host first so the steady state is
// a single request per call.
func (a *Adapter) candidateBaseURLs() []string {
	a.mu.Lock()
	known := a.knownBaseURL
	a.mu.Unlock()
	if known == "" {
		return append([]string(nil), a.baseURLs...)
	}
	ordered := make([]string, 0, len(a.baseURLs))
	ordered = append(ordered, known)
	for _, baseURL := range a.baseURLs {
		if baseURL != known {
			ordered = append(ordered, baseURL)
		}
	}
	return ordered
}

func (a *Adapter) rememberBaseURL(baseURL string) {
	a.mu.Lock()
	a.knownBaseURL = baseURL
	a.mu.Unlock()
}

func (a *Adapter) storeLastGood(snapshot providerlimits.AccountSnapshot) {
	copied := copySnapshot(snapshot)
	a.mu.Lock()
	a.lastGood = &copied
	a.mu.Unlock()
}

// staleOrUnavailable keeps the last useful reading visible while naming the
// reason it could not be refreshed.
func (a *Adapter) staleOrUnavailable(checkedAt time.Time, reason string) []providerlimits.AccountSnapshot {
	a.mu.Lock()
	lastGood := a.lastGood
	a.mu.Unlock()
	if lastGood == nil {
		return []providerlimits.AccountSnapshot{unavailableSnapshot(checkedAt, reason)}
	}
	copied := copySnapshot(*lastGood)
	copied.CheckedAt = checkedAt
	copied.Status = providerlimits.StatusStale
	copied.ErrorNote = reason
	for index := range copied.Buckets {
		copied.Buckets[index].Status = providerlimits.StatusStale
	}
	return []providerlimits.AccountSnapshot{copied}
}

func unavailableSnapshot(checkedAt time.Time, reason string) providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:   provider,
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     providerlimits.StatusUnavailable,
		Source:     snapshotSource(),
		ErrorNote:  reason,
	}
}

func snapshotSource() providerlimits.Source {
	return providerlimits.Source{
		Kind:             providerlimits.SourceKindOfficialAPI,
		FreshnessSeconds: int64(defaultFreshness / time.Second),
		Confidence:       providerlimits.ConfidenceOfficial,
	}
}

func copySnapshot(snapshot providerlimits.AccountSnapshot) providerlimits.AccountSnapshot {
	copied := snapshot
	copied.Buckets = make([]providerlimits.Bucket, len(snapshot.Buckets))
	for index, bucket := range snapshot.Buckets {
		copied.Buckets[index] = bucket
		copied.Buckets[index].LimitValue = copyNumber(bucket.LimitValue)
		copied.Buckets[index].UsedValue = copyNumber(bucket.UsedValue)
		copied.Buckets[index].RemainingValue = copyNumber(bucket.RemainingValue)
		copied.Buckets[index].ResetsAt = copyTime(bucket.ResetsAt)
	}
	return copied
}

func copyNumber(input *float64) *float64 {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}

func copyTime(input *time.Time) *time.Time {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}

// Package factory implements the verified Factory billing-limits contract:
// one GET to app.factory.ai/api/billing/limits per configured credential,
// without userId discovery or the historical api.factory.ai flow.
package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

const (
	defaultEndpoint  = "https://app.factory.ai/api/billing/limits"
	defaultFreshness = 15 * time.Minute
)

var ErrRateLimited = errors.New("factory usage rate limited")

type Credential struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	AccountLabel string `json:"account_label,omitempty"`
}

type Config struct {
	Endpoint    string
	Client      *http.Client
	Now         func() time.Time
	Credentials []Credential
}

type Adapter struct {
	endpoint string
	client   *http.Client
	now      func() time.Time

	mu          sync.RWMutex
	credentials []Credential
	lastGood    map[string]providerlimits.AccountSnapshot
}

func NewAdapter(config Config) *Adapter {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Adapter{
		endpoint:    endpoint,
		client:      client,
		now:         now,
		credentials: copyCredentials(config.Credentials),
		lastGood:    make(map[string]providerlimits.AccountSnapshot),
	}
}

func (*Adapter) Provider() string { return "factory" }

func (*Adapter) Capabilities() providerlimits.Capabilities {
	return providerlimits.Capabilities{Timeout: 10 * time.Second, MinimumInterval: defaultFreshness}
}

func (a *Adapter) ReplaceCredentials(credentials []Credential) {
	a.mu.Lock()
	a.credentials = copyCredentials(credentials)
	a.mu.Unlock()
}

func (a *Adapter) Collect(ctx context.Context) ([]providerlimits.AccountSnapshot, error) {
	checkedAt := a.now().UTC()
	credentials := a.credentialsSnapshot()
	if len(credentials) == 0 {
		return []providerlimits.AccountSnapshot{unavailableSnapshot("unavailable", "", checkedAt, "credential_missing")}, nil
	}

	snapshots := make([]providerlimits.AccountSnapshot, 0, len(credentials))
	var collectionErrors []error
	for _, credential := range credentials {
		snapshot, err := a.collectCredential(ctx, credential, checkedAt)
		snapshots = append(snapshots, snapshot)
		if err != nil {
			collectionErrors = append(collectionErrors, err)
		}
	}
	for _, err := range collectionErrors {
		if errors.Is(err, ErrRateLimited) {
			return snapshots, ErrRateLimited
		}
	}
	return snapshots, errors.Join(collectionErrors...)
}

func (a *Adapter) collectCredential(ctx context.Context, credential Credential, checkedAt time.Time) (providerlimits.AccountSnapshot, error) {
	accountKey := accountKeyFrom(credential.ID)
	if strings.TrimSpace(credential.Token) == "" || accountKey == "unavailable" {
		return unavailableSnapshot(accountKey, credential.AccountLabel, checkedAt, "credential_invalid"), nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return a.staleOrUnavailable(accountKey, credential.AccountLabel, checkedAt, "usage_unavailable"), errors.New("factory usage request unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	request.Header.Set("Accept", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return a.staleOrUnavailable(accountKey, credential.AccountLabel, checkedAt, "usage_unavailable"), errors.New("factory usage request unavailable")
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return unavailableSnapshot(accountKey, credential.AccountLabel, checkedAt, "credential_invalid"), nil
	case http.StatusTooManyRequests:
		return a.staleOrUnavailable(accountKey, credential.AccountLabel, checkedAt, "rate_limited"), ErrRateLimited
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return a.staleOrUnavailable(accountKey, credential.AccountLabel, checkedAt, "usage_unavailable"), errors.New("factory usage request unavailable")
	}

	var limits limitsResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&limits); err != nil {
		return a.staleOrUnavailable(accountKey, credential.AccountLabel, checkedAt, "usage_unavailable"), errors.New("factory usage response unavailable")
	}
	snapshot := snapshotFromLimits(limits, accountKey, credential.AccountLabel, checkedAt)
	if snapshot.Status == providerlimits.StatusOK {
		a.storeLastGood(snapshot)
	}
	return snapshot, nil
}

type limitsResponse struct {
	UsesTokenRateLimitsBilling *bool          `json:"usesTokenRateLimitsBilling"`
	Limits                     limitFamilies  `json:"limits"`
	ExtraUsageBalanceCents     flexibleNumber `json:"extraUsageBalanceCents"`
	ExtraUsageAllowed          *bool          `json:"extraUsageAllowed"`
	OveragePreference          string         `json:"overagePreference"`
}

type limitFamilies struct {
	Standard rollingLimits `json:"standard"`
	Core     rollingLimits `json:"core"`
}

type rollingLimits struct {
	FiveHour *rollingWindow `json:"fiveHour"`
	Weekly   *rollingWindow `json:"weekly"`
	Monthly  *rollingWindow `json:"monthly"`
}

type rollingWindow struct {
	UsedPercent      flexibleNumber  `json:"usedPercent"`
	WindowEnd        json.RawMessage `json:"windowEnd"`
	SecondsRemaining flexibleNumber  `json:"secondsRemaining"`
}

type flexibleNumber struct {
	Value float64
	Valid bool
}

func (number *flexibleNumber) UnmarshalJSON(raw []byte) error {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		*number = flexibleNumber{}
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		*number = flexibleNumber{}
		return nil
	}
	*number = flexibleNumber{Value: parsed, Valid: true}
	return nil
}

func snapshotFromLimits(limits limitsResponse, accountKey, accountLabel string, checkedAt time.Time) providerlimits.AccountSnapshot {
	snapshot := providerlimits.AccountSnapshot{
		Provider:     "factory",
		AccountKey:   accountKey,
		AccountLabel: strings.TrimSpace(accountLabel),
		CheckedAt:    checkedAt,
		Status:       providerlimits.StatusOK,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindOfficialAPI,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		Buckets: make([]providerlimits.Bucket, 0, 7),
	}
	if limits.UsesTokenRateLimitsBilling == nil || !*limits.UsesTokenRateLimitsBilling {
		snapshot.Status = providerlimits.StatusUnavailable
		snapshot.ErrorNote = "legacy_plan"
		return snapshot
	}

	snapshot.Buckets = appendRollingBuckets(snapshot.Buckets, "standard", "Standard", limits.Limits.Standard, checkedAt)
	snapshot.Buckets = appendRollingBuckets(snapshot.Buckets, "core", "Droid Core", limits.Limits.Core, checkedAt)
	if limits.ExtraUsageBalanceCents.Valid {
		balance := limits.ExtraUsageBalanceCents.Value / 100
		note := extraUsageNote(limits.ExtraUsageAllowed, limits.OveragePreference)
		snapshot.Buckets = append(snapshot.Buckets, providerlimits.Bucket{
			ID:             "extra_usage",
			Label:          "Extra usage balance",
			Unit:           providerlimits.UnitCurrency,
			RemainingValue: numberPointer(balance),
			Status:         providerlimits.StatusOK,
			Note:           note,
		})
	}
	if len(snapshot.Buckets) == 0 {
		snapshot.Status = providerlimits.StatusUnavailable
		snapshot.ErrorNote = "usage_unavailable"
	}
	return snapshot
}

func appendRollingBuckets(output []providerlimits.Bucket, familyID, familyLabel string, limits rollingLimits, checkedAt time.Time) []providerlimits.Bucket {
	windows := []struct {
		id     string
		label  string
		window *rollingWindow
	}{
		{id: "5h", label: "5 hour", window: limits.FiveHour},
		{id: "weekly", label: "Weekly", window: limits.Weekly},
		{id: "monthly", label: "Monthly", window: limits.Monthly},
	}
	for _, item := range windows {
		bucket, ok := bucketFromWindow(familyID+"_"+item.id, familyLabel+" "+item.label, item.window, checkedAt)
		if ok {
			output = append(output, bucket)
		}
	}
	return output
}

func bucketFromWindow(id, label string, window *rollingWindow, checkedAt time.Time) (providerlimits.Bucket, bool) {
	if window == nil || !window.UsedPercent.Valid || window.UsedPercent.Value < 0 || window.UsedPercent.Value > 100 {
		return providerlimits.Bucket{}, false
	}
	used := window.UsedPercent.Value
	resetsAt := timeFromJSON(window.WindowEnd)
	active := window.SecondsRemaining.Valid && window.SecondsRemaining.Value > 0 && resetsAt != nil && resetsAt.After(checkedAt)
	if !active {
		used = 0
		resetsAt = nil
	}
	return providerlimits.Bucket{
		ID:             id,
		Label:          label,
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(used),
		RemainingValue: numberPointer(100 - used),
		ResetsAt:       resetsAt,
		Status:         providerlimits.StatusOK,
	}, true
}

func (a *Adapter) staleOrUnavailable(accountKey, accountLabel string, checkedAt time.Time, reason string) providerlimits.AccountSnapshot {
	a.mu.RLock()
	lastGood, ok := a.lastGood[accountKey]
	a.mu.RUnlock()
	if !ok {
		return unavailableSnapshot(accountKey, accountLabel, checkedAt, reason)
	}
	copied := copySnapshot(lastGood)
	copied.CheckedAt = checkedAt
	copied.Status = providerlimits.StatusStale
	copied.ErrorNote = reason
	for index := range copied.Buckets {
		copied.Buckets[index].Status = providerlimits.StatusStale
	}
	return copied
}

func (a *Adapter) storeLastGood(snapshot providerlimits.AccountSnapshot) {
	a.mu.Lock()
	next := make(map[string]providerlimits.AccountSnapshot, len(a.lastGood)+1)
	for key, value := range a.lastGood {
		next[key] = value
	}
	next[snapshot.AccountKey] = copySnapshot(snapshot)
	a.lastGood = next
	a.mu.Unlock()
}

func (a *Adapter) credentialsSnapshot() []Credential {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return copyCredentials(a.credentials)
}

func unavailableSnapshot(accountKey, accountLabel string, checkedAt time.Time, reason string) providerlimits.AccountSnapshot {
	return providerlimits.AccountSnapshot{
		Provider:     "factory",
		AccountKey:   accountKey,
		AccountLabel: strings.TrimSpace(accountLabel),
		CheckedAt:    checkedAt,
		Status:       providerlimits.StatusUnavailable,
		Source: providerlimits.Source{
			Kind:             providerlimits.SourceKindOfficialAPI,
			FreshnessSeconds: int64(defaultFreshness / time.Second),
			Confidence:       providerlimits.ConfidenceOfficial,
		},
		ErrorNote: reason,
	}
}

func accountKeyFrom(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unavailable"
	}
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])[:16]
}

func extraUsageNote(allowed *bool, preference string) string {
	parts := make([]string, 0, 2)
	if allowed != nil {
		if *allowed {
			parts = append(parts, "allowed")
		} else {
			parts = append(parts, "not_allowed")
		}
	}
	if value := strings.ToLower(strings.TrimSpace(preference)); value != "" {
		parts = append(parts, "preference_"+safeNoteValue(value))
	}
	return strings.Join(parts, "_")
}

func safeNoteValue(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func timeFromJSON(raw json.RawMessage) *time.Time {
	value := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if value == "" || value == "null" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func copyCredentials(input []Credential) []Credential {
	return append([]Credential(nil), input...)
}

func copySnapshot(snapshot providerlimits.AccountSnapshot) providerlimits.AccountSnapshot {
	copied := snapshot
	copied.Buckets = append([]providerlimits.Bucket(nil), snapshot.Buckets...)
	for index := range copied.Buckets {
		copied.Buckets[index] = copyBucket(copied.Buckets[index])
	}
	return copied
}

func copyBucket(bucket providerlimits.Bucket) providerlimits.Bucket {
	copied := bucket
	copied.LimitValue = copyNumber(bucket.LimitValue)
	copied.UsedValue = copyNumber(bucket.UsedValue)
	copied.RemainingValue = copyNumber(bucket.RemainingValue)
	if bucket.ResetsAt != nil {
		value := *bucket.ResetsAt
		copied.ResetsAt = &value
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

func numberPointer(value float64) *float64 {
	return &value
}

func (c Credential) String() string {
	return fmt.Sprintf("factory credential %s", accountKeyFrom(c.ID))
}

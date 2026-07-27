package antigravity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

// sessionBucketID and sessionBucketLabel are fixed. The bucket set must not
// change between snapshots, otherwise bucket history accumulates drifting
// series for the same account.
const (
	sessionBucketID    = "session"
	sessionBucketLabel = "Limit session"
)

// keyringCredential is the go-keyring blob written by `agy`. Only the access
// token and its expiry are modeled: refresh_token is intentionally absent so it
// cannot be read, logged, or forwarded by accident.
type keyringCredential struct {
	Token keyringToken `json:"token"`
}

type keyringToken struct {
	AccessToken string `json:"access_token"`
	Expiry      string `json:"expiry"`
}

type loadCodeAssistRequest struct {
	Metadata requestMetadata `json:"metadata"`
}

type requestMetadata struct {
	IDEType string `json:"ideType"`
}

// fetchAvailableModelsRequest carries the project and nothing else; the
// endpoint rejects an extra "metadata" field with 400 Unknown name.
type fetchAvailableModelsRequest struct {
	Project string `json:"project"`
}

// codeAssistResponse models only the fields this adapter needs.
// manageSubscriptionUri is deliberately not modeled: it embeds the account
// email.
type codeAssistResponse struct {
	CloudaicompanionProject string `json:"cloudaicompanionProject"`
	CurrentTier             tier   `json:"currentTier"`
	PaidTier                tier   `json:"paidTier"`
}

type tier struct {
	Name string `json:"name"`
}

// availableModelsResponse keys models by id. A shape change (for example an
// array) fails the decode, which the caller reports as usage_unavailable.
type availableModelsResponse struct {
	Models map[string]modelEntry `json:"models"`
}

type modelEntry struct {
	IsInternal bool      `json:"isInternal"`
	QuotaInfo  quotaInfo `json:"quotaInfo"`
}

type quotaInfo struct {
	RemainingFraction flexibleNumber  `json:"remainingFraction"`
	ResetTime         json.RawMessage `json:"resetTime"`
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

// sessionBucket folds every eligible model into the one account-wide pool the
// endpoint actually meters. Models share a single remainingFraction and
// resetTime, so the lowest fraction is the account's real consumption and the
// earliest resetTime is when it recovers.
func sessionBucket(models map[string]modelEntry) (providerlimits.Bucket, bool) {
	lowestFraction := math.Inf(1)
	var earliestReset *time.Time
	for _, entry := range models {
		if entry.IsInternal {
			continue
		}
		fraction := entry.QuotaInfo.RemainingFraction
		resetsAt := timeFromJSON(entry.QuotaInfo.ResetTime)
		if resetsAt == nil || !fraction.Valid || fraction.Value < 0 || fraction.Value > 1 {
			continue
		}
		lowestFraction = math.Min(lowestFraction, fraction.Value)
		if earliestReset == nil || resetsAt.Before(*earliestReset) {
			earliestReset = resetsAt
		}
	}
	if math.IsInf(lowestFraction, 1) {
		return providerlimits.Bucket{}, false
	}
	used := math.Round((1 - lowestFraction) * 100)
	if used <= 0 {
		// resetTime slides forward while the window has not started, so a reset
		// hint on an untouched quota would show a moving number with no meaning.
		used = 0
		earliestReset = nil
	}
	return providerlimits.Bucket{
		ID:             sessionBucketID,
		Label:          sessionBucketLabel,
		Unit:           providerlimits.UnitPercent,
		LimitValue:     numberPointer(100),
		UsedValue:      numberPointer(used),
		RemainingValue: numberPointer(100 - used),
		ResetsAt:       earliestReset,
		Status:         providerlimits.StatusOK,
	}, true
}

func planName(assist codeAssistResponse) string {
	if name := strings.TrimSpace(assist.PaidTier.Name); name != "" {
		return name
	}
	return strings.TrimSpace(assist.CurrentTier.Name)
}

// accountKeyFrom hashes the Cloud Code project so accounts stay distinguishable
// without the raw identifier crossing the daemon boundary.
func accountKeyFrom(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return "unavailable"
	}
	hash := sha256.Sum256([]byte(project))
	return hex.EncodeToString(hash[:])[:16]
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

func numberPointer(value float64) *float64 {
	return &value
}

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

// Bucket ids are fixed. The bucket set must not change between snapshots,
// otherwise bucket history accumulates drifting series for the same account.
//
// Antigravity meters two independent pools, confirmed by a controlled quota
// check: one `agy` call on a Claude model moved the claude/gpt-oss fraction and
// its resetTime while the gemini fraction and resetTime stood still. The pools
// are reported per model, so the model id is the only signal that says which
// pool a reading belongs to.
const (
	claudeBucketID    = "session_claude"
	claudeBucketLabel = "Limit session Claude"
	geminiBucketID    = "session_gemini"
	geminiBucketLabel = "Limit session Gemini"
)

// bucketOrder controls display order and is the complete set of families this
// adapter can emit.
var bucketOrder = []struct {
	id    string
	label string
}{
	{id: claudeBucketID, label: claudeBucketLabel},
	{id: geminiBucketID, label: geminiBucketLabel},
}

// claudeFamilyPrefixes lists the model id prefixes observed sharing the
// non-Gemini pool. Anything else falls in with Gemini: that is what every
// observed non-Claude model did, and since a family reports its lowest
// fraction, an unrecognized model can only ever make its bar more pessimistic,
// never hide consumption.
var claudeFamilyPrefixes = []string{"claude-", "gpt-oss-"}

func familyBucketID(modelID string) string {
	lowered := strings.ToLower(strings.TrimSpace(modelID))
	for _, prefix := range claudeFamilyPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return claudeBucketID
		}
	}
	return geminiBucketID
}

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

// familyReading accumulates the worst reading seen inside one quota family.
type familyReading struct {
	lowestFraction float64
	earliestReset  *time.Time
	seen           bool
}

// sessionBuckets reports one bucket per quota family. Within a family the
// lowest fraction is the real consumption and the earliest resetTime is when it
// recovers; across families nothing is combined, because they drain and reset
// independently. Returns false only when no family produced a usable reading.
func sessionBuckets(models map[string]modelEntry) ([]providerlimits.Bucket, bool) {
	readings := make(map[string]*familyReading, len(bucketOrder))
	for modelID, entry := range models {
		if entry.IsInternal {
			continue
		}
		fraction := entry.QuotaInfo.RemainingFraction
		resetsAt := timeFromJSON(entry.QuotaInfo.ResetTime)
		if resetsAt == nil || !fraction.Valid || fraction.Value < 0 || fraction.Value > 1 {
			continue
		}
		bucketID := familyBucketID(modelID)
		reading, ok := readings[bucketID]
		if !ok {
			reading = &familyReading{lowestFraction: math.Inf(1)}
			readings[bucketID] = reading
		}
		reading.seen = true
		reading.lowestFraction = math.Min(reading.lowestFraction, fraction.Value)
		if reading.earliestReset == nil || resetsAt.Before(*reading.earliestReset) {
			reading.earliestReset = resetsAt
		}
	}

	buckets := make([]providerlimits.Bucket, 0, len(bucketOrder))
	for _, entry := range bucketOrder {
		reading, ok := readings[entry.id]
		if !ok || !reading.seen {
			continue
		}
		used := math.Round((1 - reading.lowestFraction) * 100)
		resetsAt := reading.earliestReset
		if used <= 0 {
			// resetTime slides forward while the window has not started, so a reset
			// hint on an untouched quota would show a moving number with no meaning.
			used = 0
			resetsAt = nil
		}
		buckets = append(buckets, providerlimits.Bucket{
			ID:             entry.id,
			Label:          entry.label,
			Unit:           providerlimits.UnitPercent,
			LimitValue:     numberPointer(100),
			UsedValue:      numberPointer(used),
			RemainingValue: numberPointer(100 - used),
			ResetsAt:       resetsAt,
			Status:         providerlimits.StatusOK,
		})
	}
	return buckets, len(buckets) > 0
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

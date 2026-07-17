package providerlimits

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultMaxSnapshots = 32
	defaultMaxBuckets   = 32
	defaultMaxText      = 240
)

var (
	tokenLikeText = regexp.MustCompile(`(?i)(bearer\s+\S+|\bsk-[a-z0-9_-]+|\b(?:access[_-]?token|refresh[_-]?token|api[_-]?key)\b|eyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+)`)
	authPathText  = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|/)(?:[^\s]*)(?:\.claude|\.codex|\.config)[\\/][^\s]*(?:credentials|auth)[^\s]*`)
	reasonCode    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	providerID    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	accountKey    = regexp.MustCompile(`^(?:[a-f0-9]{8,64}|unavailable)$`)
	accountLabel  = regexp.MustCompile(`(?i)^(?:[a-z0-9*._-]+@[a-z0-9.-]+|profile-[a-z0-9_-]{1,48})$`)
	bucketLabel   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _-]{0,63}$`)
)

// SanitizationCaps bound the amount of trusted adapter data that can cross
// into transport. Non-positive fields use conservative defaults.
type SanitizationCaps struct {
	MaxSnapshots          int
	MaxBucketsPerSnapshot int
	MaxTextLength         int
}

func (c SanitizationCaps) normalized() SanitizationCaps {
	if c.MaxSnapshots <= 0 {
		c.MaxSnapshots = defaultMaxSnapshots
	}
	if c.MaxBucketsPerSnapshot <= 0 {
		c.MaxBucketsPerSnapshot = defaultMaxBuckets
	}
	if c.MaxTextLength <= 0 {
		c.MaxTextLength = defaultMaxText
	}
	return c
}

// SanitizeSnapshots returns copied, bounded snapshots. Any text that looks
// like a token or names a local auth file is omitted rather than redacted in
// place, so partial credentials and raw output cannot escape the daemon.
func SanitizeSnapshots(input []AccountSnapshot, caps SanitizationCaps) []AccountSnapshot {
	caps = caps.normalized()
	limit := min(len(input), caps.MaxSnapshots)
	output := make([]AccountSnapshot, 0, limit)
	for _, snapshot := range input[:limit] {
		copied := snapshot
		copied.Provider = safeIdentifier(snapshot.Provider, providerID, caps.MaxTextLength)
		copied.AccountKey = safeIdentifier(snapshot.AccountKey, accountKey, caps.MaxTextLength)
		copied.AccountLabel = safeAccountLabel(snapshot.AccountLabel, caps.MaxTextLength)
		copied.ErrorNote = safeReason(snapshot.ErrorNote)
		copied.Status = safeStatus(snapshot.Status)
		copied.Source = Source{
			Kind:             safeSourceKind(snapshot.Source.Kind),
			FreshnessSeconds: max(snapshot.Source.FreshnessSeconds, 0),
			Confidence:       safeConfidence(snapshot.Source.Confidence),
		}

		bucketLimit := min(len(snapshot.Buckets), caps.MaxBucketsPerSnapshot)
		copied.Buckets = make([]Bucket, bucketLimit)
		for index, bucket := range snapshot.Buckets[:bucketLimit] {
			copied.Buckets[index] = Bucket{
				ID:             safeIdentifier(bucket.ID, reasonCode, caps.MaxTextLength),
				Label:          safeIdentifier(bucket.Label, bucketLabel, caps.MaxTextLength),
				Unit:           safeUnit(bucket.Unit),
				LimitValue:     cloneNumber(bucket.LimitValue),
				UsedValue:      cloneNumber(bucket.UsedValue),
				RemainingValue: cloneNumber(bucket.RemainingValue),
				ResetsAt:       cloneTime(bucket.ResetsAt),
				Status:         safeStatus(bucket.Status),
				Note:           safeReason(bucket.Note),
			}
		}
		output = append(output, copied)
	}
	return output
}

func safeReason(value string) string {
	return safeIdentifier(value, reasonCode, defaultMaxText)
}

func safeAccountLabel(value string, maxLength int) string {
	label := safeIdentifier(value, accountLabel, maxLength)
	if label == "" || strings.HasPrefix(strings.ToLower(label), "profile-") {
		return label
	}
	localPart, _, found := strings.Cut(label, "@")
	if !found || !strings.Contains(localPart, "*") {
		return ""
	}
	return label
}

func safeIdentifier(value string, pattern *regexp.Regexp, maxLength int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || tokenLikeText.MatchString(trimmed) || authPathText.MatchString(filepath.ToSlash(trimmed)) || !pattern.MatchString(trimmed) {
		return ""
	}
	if len(trimmed) > maxLength {
		return trimmed[:maxLength]
	}
	return trimmed
}

func safeStatus(value Status) Status {
	switch value {
	case StatusOK, StatusStale, StatusPartial, StatusUnavailable, StatusError:
		return value
	default:
		return StatusError
	}
}

func safeSourceKind(value SourceKind) SourceKind {
	switch value {
	case SourceKindOfficialAPI, SourceKindCLI, SourceKindLocalAuthState, SourceKindLocalLog:
		return value
	default:
		return SourceKindCLI
	}
}

func safeConfidence(value Confidence) Confidence {
	switch value {
	case ConfidenceOfficial, ConfidenceObserved, ConfidenceEstimated:
		return value
	default:
		return ConfidenceObserved
	}
}

func safeUnit(value Unit) Unit {
	switch value {
	case UnitPercent, UnitTokens, UnitCredits, UnitCurrency, UnitRequests:
		return value
	default:
		return UnitPercent
	}
}

func safeText(value string, maxLength int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || tokenLikeText.MatchString(trimmed) || authPathText.MatchString(filepath.ToSlash(trimmed)) {
		return ""
	}
	if len(trimmed) > maxLength {
		return trimmed[:maxLength]
	}
	return trimmed
}

func cloneNumber(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

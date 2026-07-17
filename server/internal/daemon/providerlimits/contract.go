// Package providerlimits collects safe, normalized provider-limit snapshots on
// the trusted daemon host. Provider-specific adapters must never return raw
// responses, credentials, or local auth paths through this package boundary.
package providerlimits

import (
	"context"
	"time"
)

// Status describes the availability and completeness of a normalized snapshot
// or bucket. Staleness is derived by the backend at read time and is included
// for transport compatibility with already-derived snapshots only.
type Status string

const (
	StatusOK          Status = "ok"
	StatusStale       Status = "stale"
	StatusPartial     Status = "partial"
	StatusUnavailable Status = "unavailable"
	StatusError       Status = "error"
)

// SourceKind describes how an adapter obtained a snapshot.
type SourceKind string

const (
	SourceKindOfficialAPI    SourceKind = "official_api"
	SourceKindCLI            SourceKind = "cli"
	SourceKindLocalAuthState SourceKind = "local_auth_state"
	SourceKindLocalLog       SourceKind = "local_log"
)

// Confidence communicates whether a snapshot is authoritative, observed, or
// derived from an estimate.
type Confidence string

const (
	ConfidenceOfficial  Confidence = "official"
	ConfidenceObserved  Confidence = "observed"
	ConfidenceEstimated Confidence = "estimated"
)

// Unit identifies the measurement represented by a quota bucket.
type Unit string

const (
	UnitPercent  Unit = "percent"
	UnitTokens   Unit = "tokens"
	UnitCredits  Unit = "credits"
	UnitCurrency Unit = "currency"
	UnitRequests Unit = "requests"
)

// Source records safe provenance metadata for a snapshot.
type Source struct {
	Kind             SourceKind `json:"kind"`
	FreshnessSeconds int64      `json:"freshness_seconds,omitempty"`
	Confidence       Confidence `json:"confidence"`
}

// Bucket is one independent provider quota or credit pool. Pointer values
// distinguish an unavailable metric from a real zero.
type Bucket struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Unit           Unit       `json:"unit"`
	LimitValue     *float64   `json:"limit_value,omitempty"`
	UsedValue      *float64   `json:"used_value,omitempty"`
	RemainingValue *float64   `json:"remaining_value,omitempty"`
	ResetsAt       *time.Time `json:"resets_at,omitempty"`
	Status         Status     `json:"status"`
	Note           string     `json:"note,omitempty"`
}

// AccountSnapshot is the only data an adapter may offer to the transport
// layer. AccountKey is a lower-case SHA-256 prefix or "unavailable";
// AccountLabel is a masked email or a normalized profile-* label. It
// intentionally contains no raw provider response, credential, or local source
// path. Treat values as immutable: sanitization always returns copied values
// rather than modifying adapter-owned input.
type AccountSnapshot struct {
	Provider     string    `json:"provider"`
	AccountKey   string    `json:"account_key"`
	AccountLabel string    `json:"account_label,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
	Status       Status    `json:"status"`
	Source       Source    `json:"source"`
	Buckets      []Bucket  `json:"buckets"`
	ErrorNote    string    `json:"error_note,omitempty"`
}

// Capabilities are the stable scheduling characteristics an adapter declares
// to the collector. They make timeouts and provider-specific rate limits part
// of the adapter contract rather than hidden implementation details.
type Capabilities struct {
	Timeout         time.Duration
	MinimumInterval time.Duration
}

// Adapter supplies normalized snapshots for exactly one provider.
type Adapter interface {
	Provider() string
	Capabilities() Capabilities
	Collect(context.Context) ([]AccountSnapshot, error)
}

// Reporter sends a sanitized normalized batch across the daemon boundary.
type Reporter interface {
	Report(context.Context, []AccountSnapshot) error
}

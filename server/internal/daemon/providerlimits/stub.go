package providerlimits

import (
	"context"
	"time"
)

// UnavailableStub is a safe placeholder for a provider whose local probe has
// not been implemented or validated yet. It gives the UI an honest
// unavailable state without accessing local files or making network calls.
type UnavailableStub struct {
	provider string
}

// NewUnavailableStub constructs a no-I/O adapter for the named provider.
func NewUnavailableStub(provider string) UnavailableStub {
	return UnavailableStub{provider: provider}
}

func (a UnavailableStub) Provider() string { return a.provider }

func (UnavailableStub) Capabilities() Capabilities {
	return Capabilities{MinimumInterval: 15 * time.Minute}
}

func (a UnavailableStub) Collect(context.Context) ([]AccountSnapshot, error) {
	return []AccountSnapshot{{
		Provider:   a.provider,
		AccountKey: "unavailable",
		CheckedAt:  time.Now().UTC(),
		Status:     StatusUnavailable,
		Source: Source{
			Kind:       SourceKindCLI,
			Confidence: ConfidenceObserved,
		},
		ErrorNote: "adapter_unavailable",
	}}, nil
}

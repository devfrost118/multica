package daemon

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/daemon/providerlimits"
)

// providerLimitsReporter fans one daemon-local collection out to the runtime
// rows currently registered for each workspace. Server-side read APIs dedupe
// by stable account key; this layer intentionally retains runtime identity for
// the diagnostic view.
type providerLimitsReporter struct {
	client     *Client
	runtimeIDs func() []string
}

func (r providerLimitsReporter) Report(ctx context.Context, snapshots []providerlimits.AccountSnapshot) error {
	errorsByRuntime := make([]error, 0)
	for _, runtimeID := range r.runtimeIDs() {
		if err := r.client.ReportProviderLimits(ctx, runtimeID, snapshots); err != nil {
			errorsByRuntime = append(errorsByRuntime, err)
		}
	}
	return errors.Join(errorsByRuntime...)
}

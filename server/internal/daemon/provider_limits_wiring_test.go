package daemon

import (
	"log/slog"
	"testing"
)

func TestNewInitializesProviderLimitsCollector(t *testing.T) {
	t.Parallel()

	daemon := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "http://example.test"}, slog.Default())
	if daemon.providerLimits == nil {
		t.Fatal("New() must initialize the provider limits collector")
	}
}

package providerlimits

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type adapterFunc struct {
	provider string
	caps     Capabilities
	collect  func(context.Context) ([]AccountSnapshot, error)
}

type metadataAdapter struct {
	provider     func() string
	capabilities func() Capabilities
	collect      func(context.Context) ([]AccountSnapshot, error)
}

func (a metadataAdapter) Provider() string { return a.provider() }

func (a metadataAdapter) Capabilities() Capabilities { return a.capabilities() }

func (a metadataAdapter) Collect(ctx context.Context) ([]AccountSnapshot, error) {
	return a.collect(ctx)
}

func (a adapterFunc) Provider() string { return a.provider }

func (a adapterFunc) Capabilities() Capabilities { return a.caps }

func (a adapterFunc) Collect(ctx context.Context) ([]AccountSnapshot, error) {
	return a.collect(ctx)
}

type reporterFunc func(context.Context, []AccountSnapshot) error

func (f reporterFunc) Report(ctx context.Context, snapshots []AccountSnapshot) error {
	return f(ctx, snapshots)
}

func testSnapshot(provider string) AccountSnapshot {
	return AccountSnapshot{
		Provider:     provider,
		AccountKey:   "a1b2c3d4",
		AccountLabel: "profile-default",
		CheckedAt:    time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
		Status:       StatusOK,
		Source: Source{
			Kind:       SourceKindLocalLog,
			Confidence: ConfidenceObserved,
		},
		Buckets: []Bucket{{ID: "weekly", Label: "Weekly", Unit: UnitPercent, UsedValue: float64Ptr(20)}},
	}
}

func float64Ptr(value float64) *float64 { return &value }

func TestCollector_ContainsAdapterPanicAndReportsOtherSnapshots(t *testing.T) {
	t.Parallel()

	var got []AccountSnapshot
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{
			adapterFunc{provider: "panic-provider", collect: func(context.Context) ([]AccountSnapshot, error) {
				panic("Bearer top-secret-token")
			}},
			adapterFunc{provider: "healthy-provider", collect: func(context.Context) ([]AccountSnapshot, error) {
				return []AccountSnapshot{testSnapshot("healthy-provider")}, nil
			}},
		},
		Reporter: reporterFunc(func(_ context.Context, snapshots []AccountSnapshot) error {
			got = snapshots
			return nil
		}),
		Now: func() time.Time { return time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC) },
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("reported snapshots = %d, want 2", len(got))
	}
	if got[0].Status != StatusError || got[0].Provider != "panic-provider" {
		t.Fatalf("panic snapshot = %#v, want sanitized error for panic-provider", got[0])
	}
	if got[0].ErrorNote != "collection_failed" {
		t.Fatalf("panic error note = %q", got[0].ErrorNote)
	}
	if got[1].Provider != "healthy-provider" || got[1].Status != StatusOK {
		t.Fatalf("healthy snapshot = %#v", got[1])
	}
}

func TestCollector_TimeoutDoesNotBlockOtherAdapters(t *testing.T) {
	t.Parallel()

	var got []AccountSnapshot
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{
			adapterFunc{provider: "slow-provider", caps: Capabilities{Timeout: 5 * time.Millisecond}, collect: func(ctx context.Context) ([]AccountSnapshot, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}},
			adapterFunc{provider: "healthy-provider", collect: func(context.Context) ([]AccountSnapshot, error) {
				return []AccountSnapshot{testSnapshot("healthy-provider")}, nil
			}},
		},
		Reporter: reporterFunc(func(_ context.Context, snapshots []AccountSnapshot) error {
			got = snapshots
			return nil
		}),
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce() error = %v", err)
	}
	if len(got) != 2 || got[0].Status != StatusError || got[1].Status != StatusOK {
		t.Fatalf("reported snapshots = %#v", got)
	}
}

func TestCollector_ContainsPanicsFromAdapterMetadata(t *testing.T) {
	t.Parallel()

	var got []AccountSnapshot
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{
			metadataAdapter{
				provider: func() string { panic("Bearer provider-secret") },
				capabilities: func() Capabilities {
					return Capabilities{}
				},
				collect: func(context.Context) ([]AccountSnapshot, error) { return nil, nil },
			},
			metadataAdapter{
				provider: func() string { return "caps-provider" },
				capabilities: func() Capabilities {
					panic("Bearer capabilities-secret")
				},
				collect: func(context.Context) ([]AccountSnapshot, error) { return nil, nil },
			},
		},
		Reporter: reporterFunc(func(_ context.Context, snapshots []AccountSnapshot) error {
			got = snapshots
			return nil
		}),
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("CollectOnce() leaked metadata panic: %v", recovered)
		}
	}()
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce() error = %v", err)
	}
	if len(got) != 2 || got[0].Status != StatusError || got[1].Status != StatusError {
		t.Fatalf("metadata panic snapshots = %#v", got)
	}
}

func TestCollector_DoesNotRelaunchTimedOutAdapterWhileItIsStillRunning(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	attempts := 0
	started := make(chan struct{})
	release := make(chan struct{})
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{adapterFunc{provider: "stuck-provider", caps: Capabilities{Timeout: 5 * time.Millisecond}, collect: func(context.Context) ([]AccountSnapshot, error) {
			mu.Lock()
			attempts++
			mu.Unlock()
			close(started)
			<-release
			return nil, nil
		}}},
		Reporter: reporterFunc(func(context.Context, []AccountSnapshot) error { return nil }),
		Backoff:  BackoffConfig{Base: time.Nanosecond, Max: time.Nanosecond},
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("first CollectOnce() error = %v", err)
	}
	<-started
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("second CollectOnce() error = %v", err)
	}
	close(release)

	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("stuck adapter attempts = %d, want 1", attempts)
	}
}

func TestCollector_AppliesPerProviderBackoffAfterFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	attempts := 0
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{adapterFunc{provider: "failing-provider", collect: func(context.Context) ([]AccountSnapshot, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			return nil, errors.New("network failure")
		}}},
		Reporter: reporterFunc(func(context.Context, []AccountSnapshot) error { return nil }),
		Now:      func() time.Time { return now },
		Backoff:  BackoffConfig{Base: time.Minute, Max: 4 * time.Minute},
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("first CollectOnce() error = %v", err)
	}
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("backed off CollectOnce() error = %v", err)
	}
	now = now.Add(time.Minute)
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("retry CollectOnce() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("adapter attempts = %d, want 2", attempts)
	}
}

func TestCollector_RetainsSafeStaleSnapshotWhileBackingOffAdapterError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	attempts := 0
	stale := testSnapshot("claude")
	stale.Status = StatusStale
	stale.ErrorNote = "rate_limited"
	stale.Buckets[0].Status = StatusStale
	var reported []AccountSnapshot
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{adapterFunc{provider: "claude", collect: func(context.Context) ([]AccountSnapshot, error) {
			attempts++
			return []AccountSnapshot{stale}, errors.New("rate limited")
		}}},
		Reporter: reporterFunc(func(_ context.Context, snapshots []AccountSnapshot) error {
			reported = snapshots
			return nil
		}),
		Now:     func() time.Time { return now },
		Backoff: BackoffConfig{Base: time.Minute, Max: time.Minute},
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("first CollectOnce() error = %v", err)
	}
	if len(reported) != 1 || reported[0].Status != StatusStale || reported[0].ErrorNote != "rate_limited" {
		t.Fatalf("reported snapshots = %#v", reported)
	}
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("backed off CollectOnce() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("adapter attempts = %d, want 1 while backing off", attempts)
	}
}

func TestCollector_HonorsAdapterMinimumIntervalAfterSuccess(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	attempts := 0
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{adapterFunc{
			provider: "rate-limited-provider",
			caps:     Capabilities{MinimumInterval: 15 * time.Minute},
			collect: func(context.Context) ([]AccountSnapshot, error) {
				mu.Lock()
				defer mu.Unlock()
				attempts++
				return []AccountSnapshot{testSnapshot("rate-limited-provider")}, nil
			},
		}},
		Reporter: reporterFunc(func(context.Context, []AccountSnapshot) error { return nil }),
		Now:      func() time.Time { return now },
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("first CollectOnce() error = %v", err)
	}
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("limited CollectOnce() error = %v", err)
	}
	now = now.Add(15 * time.Minute)
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("next eligible CollectOnce() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("adapter attempts = %d, want 2", attempts)
	}
}

func TestCollector_ManualRefreshBypassesSuccessIntervalButHonorsBackoff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 19, 19, 0, 0, 0, time.UTC)
	attempts := 0
	collector := NewCollector(CollectorConfig{
		Adapters: []Adapter{adapterFunc{
			provider: "claude",
			caps:     Capabilities{MinimumInterval: 15 * time.Minute},
			collect: func(context.Context) ([]AccountSnapshot, error) {
				attempts++
				if attempts == 3 {
					return nil, errors.New("rate limited")
				}
				return []AccountSnapshot{testSnapshot("claude")}, nil
			},
		}},
		Reporter: reporterFunc(func(context.Context, []AccountSnapshot) error { return nil }),
		Now:      func() time.Time { return now },
		Backoff:  BackoffConfig{Base: time.Minute, Max: time.Minute},
	})

	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce() error = %v", err)
	}
	if err := collector.CollectOnce(context.Background()); err != nil {
		t.Fatalf("limited CollectOnce() error = %v", err)
	}
	if err := collector.CollectRefresh(context.Background()); err != nil {
		t.Fatalf("CollectRefresh() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts after manual refresh = %d, want 2", attempts)
	}
	if err := collector.CollectRefresh(context.Background()); err != nil {
		t.Fatalf("failing CollectRefresh() error = %v", err)
	}
	if err := collector.CollectRefresh(context.Background()); err != nil {
		t.Fatalf("backed off CollectRefresh() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts after backoff = %d, want 3", attempts)
	}
}

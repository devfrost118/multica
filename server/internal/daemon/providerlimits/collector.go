package providerlimits

import (
	"context"
	"errors"
	"sync"
	"time"
)

const defaultAdapterTimeout = 20 * time.Second

// BackoffConfig controls the bounded exponential retry delay for one failing
// provider. A provider failure never postpones other adapters.
type BackoffConfig struct {
	Base time.Duration
	Max  time.Duration
}

func (c BackoffConfig) normalized() BackoffConfig {
	if c.Base <= 0 {
		c.Base = time.Minute
	}
	if c.Max < c.Base {
		c.Max = 30 * time.Minute
	}
	return c
}

// CollectorConfig assembles the daemon-local collection loop.
type CollectorConfig struct {
	Adapters         []Adapter
	Reporter         Reporter
	Interval         time.Duration
	Backoff          BackoffConfig
	SanitizationCaps SanitizationCaps
	Now              func() time.Time
	OnError          func(error)
}

type providerState struct {
	failures    int
	nextAttempt time.Time
	lastSuccess time.Time
	inFlight    bool
}

// Collector runs adapters in isolation and forwards only sanitized snapshots.
type Collector struct {
	adapters         []Adapter
	reporter         Reporter
	interval         time.Duration
	backoff          BackoffConfig
	sanitizationCaps SanitizationCaps
	now              func() time.Time
	onError          func(error)

	mu     sync.Mutex
	states map[string]providerState
}

// NewCollector creates a collector with conservative defaults. A nil reporter
// is permitted for test and dry-run use; collection still executes safely.
func NewCollector(config CollectorConfig) *Collector {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	if config.Interval <= 0 {
		config.Interval = 15 * time.Minute
	}
	return &Collector{
		adapters:         append([]Adapter(nil), config.Adapters...),
		reporter:         config.Reporter,
		interval:         config.Interval,
		backoff:          config.Backoff.normalized(),
		sanitizationCaps: config.SanitizationCaps.normalized(),
		now:              now,
		onError:          config.OnError,
		states:           make(map[string]providerState),
	}
}

// Run performs an immediate collection and continues until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	if err := c.CollectOnce(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		c.reportError(err)
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.CollectOnce(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				c.reportError(err)
			}
		}
	}
}

// CollectOnce runs every eligible provider independently and emits one safe
// batch. Provider errors become a generic error snapshot; their messages are
// intentionally never copied into output or logs.
func (c *Collector) CollectOnce(ctx context.Context) error {
	now := c.now().UTC()
	collected := make([]AccountSnapshot, 0, len(c.adapters))
	for _, adapter := range c.adapters {
		if adapter == nil {
			continue
		}
		provider, capabilities, metadataErr := adapterMetadata(adapter)
		if metadataErr != nil {
			c.recordFailure(provider, now)
			collected = append(collected, failureSnapshot(provider, now))
			continue
		}
		if !c.beginAttempt(provider, now) {
			continue
		}
		snapshots, err := c.collectAdapter(ctx, provider, capabilities, adapter)
		if err != nil {
			c.recordFailure(provider, now)
			collected = append(collected, failureSnapshot(provider, now))
			continue
		}
		c.recordSuccess(provider, now, capabilities.MinimumInterval)
		collected = append(collected, normalizeSnapshots(provider, snapshots, now)...)
	}
	if len(collected) == 0 || c.reporter == nil {
		return nil
	}
	return c.reporter.Report(ctx, SanitizeSnapshots(collected, c.sanitizationCaps))
}

func (c *Collector) collectAdapter(ctx context.Context, provider string, capabilities Capabilities, adapter Adapter) ([]AccountSnapshot, error) {
	timeout := capabilities.Timeout
	if timeout <= 0 {
		timeout = defaultAdapterTimeout
	}
	adapterCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		snapshots []AccountSnapshot
		err       error
	}
	resultCh := make(chan result, 1)
	go func() {
		deferred := result{}
		defer func() {
			c.finishAttempt(provider)
			if recover() != nil {
				deferred.err = errors.New("adapter panic")
			}
			resultCh <- deferred
		}()
		deferred.snapshots, deferred.err = adapter.Collect(adapterCtx)
	}()

	select {
	case result := <-resultCh:
		return result.snapshots, result.err
	case <-adapterCtx.Done():
		return nil, adapterCtx.Err()
	}
}

func adapterMetadata(adapter Adapter) (provider string, capabilities Capabilities, err error) {
	provider = "unknown"
	defer func() {
		if recover() != nil {
			err = errors.New("adapter metadata panic")
		}
	}()
	provider = safeIdentifier(adapter.Provider(), providerID, defaultMaxText)
	if provider == "" {
		return "unknown", Capabilities{}, errors.New("adapter provider is invalid")
	}
	capabilities = adapter.Capabilities()
	return provider, capabilities, nil
}

func (c *Collector) beginAttempt(provider string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.states[provider]
	if state.inFlight || now.Before(state.nextAttempt) {
		return false
	}
	state.inFlight = true
	c.states[provider] = state
	return true
}

func (c *Collector) finishAttempt(provider string) {
	c.mu.Lock()
	state := c.states[provider]
	state.inFlight = false
	c.states[provider] = state
	c.mu.Unlock()
}

func (c *Collector) recordSuccess(provider string, now time.Time, minimumInterval time.Duration) {
	c.mu.Lock()
	state := c.states[provider]
	state.failures = 0
	state.lastSuccess = now
	state.nextAttempt = now.Add(max(minimumInterval, 0))
	c.states[provider] = state
	c.mu.Unlock()
}

func (c *Collector) recordFailure(provider string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.states[provider]
	state.failures++
	state.nextAttempt = now.Add(c.failureDelay(state.failures))
	c.states[provider] = state
}

func (c *Collector) failureDelay(failures int) time.Duration {
	delay := c.backoff.Base
	for attempt := 1; attempt < failures && delay < c.backoff.Max; attempt++ {
		delay *= 2
		if delay > c.backoff.Max {
			return c.backoff.Max
		}
	}
	return delay
}

func (c *Collector) reportError(err error) {
	if c.onError != nil {
		c.onError(err)
	}
}

func normalizeSnapshots(provider string, snapshots []AccountSnapshot, checkedAt time.Time) []AccountSnapshot {
	output := make([]AccountSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		copied := snapshot
		copied.Provider = provider
		if copied.CheckedAt.IsZero() {
			copied.CheckedAt = checkedAt
		}
		output = append(output, copied)
	}
	return output
}

func failureSnapshot(provider string, checkedAt time.Time) AccountSnapshot {
	return AccountSnapshot{
		Provider:   provider,
		AccountKey: "unavailable",
		CheckedAt:  checkedAt,
		Status:     StatusError,
		Source: Source{
			Kind:       SourceKindCLI,
			Confidence: ConfidenceObserved,
		},
		ErrorNote: "collection_failed",
	}
}

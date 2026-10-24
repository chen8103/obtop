package service

import (
	"context"
	"sync"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

// Snapshot is the TUI-facing alias for a collected sample.
// Prefer SampledAt for new code; RefreshedAt is kept for existing callers.
type Snapshot struct {
	Rows        []obmodel.Process
	SQLRates    obmodel.SQLRates
	RefreshedAt time.Time
	Err         error
	MetricsErr  error
}

type Monitor struct {
	sampler  *Sampler
	interval time.Duration
	updates  chan Snapshot
	refresh  chan struct{}
	reset    chan time.Duration

	mu     sync.RWMutex
	latest Snapshot
}

func NewMonitor(fetcher Fetcher, interval time.Duration) *Monitor {
	return &Monitor{
		sampler:  NewSampler(fetcher),
		interval: interval,
		updates:  make(chan Snapshot, 8),
		refresh:  make(chan struct{}, 1),
		reset:    make(chan time.Duration, 1),
	}
}

func (m *Monitor) Updates() <-chan Snapshot {
	return m.updates
}

func (m *Monitor) Latest() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneSnapshot(m.latest)
}

func (m *Monitor) Start(ctx context.Context) {
	go m.run(ctx)
}

func (m *Monitor) TriggerRefresh() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}

func (m *Monitor) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}

	m.mu.Lock()
	m.interval = interval
	m.mu.Unlock()

	select {
	case m.reset <- interval:
	default:
		select {
		case <-m.reset:
		default:
		}
		m.reset <- interval
	}
}

func (m *Monitor) run(ctx context.Context) {
	defer close(m.updates)

	ticker := time.NewTicker(m.currentInterval())
	defer ticker.Stop()

	m.fetchAndPublish(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.fetchAndPublish(ctx)
		case <-m.refresh:
			m.fetchAndPublish(ctx)
		case interval := <-m.reset:
			ticker.Stop()
			ticker = time.NewTicker(interval)
		}
	}
}

func (m *Monitor) fetchAndPublish(ctx context.Context) {
	sampled := m.sampler.Sample(ctx, SampleOptions{})
	snapshot := Snapshot{
		Rows:        sampled.Rows,
		SQLRates:    sampled.SQLRates,
		RefreshedAt: sampled.SampledAt,
		Err:         sampled.SessionsErr,
		MetricsErr:  sampled.MetricsErr,
	}

	m.mu.Lock()
	m.latest = cloneSnapshot(snapshot)
	m.mu.Unlock()

	// Lossy publish for TUI: drop oldest when buffer is full.
	select {
	case m.updates <- snapshot:
	default:
		select {
		case <-m.updates:
		default:
		}
		m.updates <- snapshot
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	rows := make([]obmodel.Process, len(snapshot.Rows))
	copy(rows, snapshot.Rows)
	snapshot.Rows = rows
	snapshot.SQLRates = obmodel.CloneSQLRates(snapshot.SQLRates)
	return snapshot
}

func (m *Monitor) currentInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

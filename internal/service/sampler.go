package service

import (
	"context"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

// Fetcher loads processlist rows and SQL counters for one sample.
type Fetcher interface {
	FetchProcesses(context.Context) ([]obmodel.Process, error)
	FetchSQLCounters(context.Context) ([]obmodel.SQLCounter, error)
}

// Sampler collects one Snapshot at a time. Each Sampler owns an independent
// rate calculator baseline so concurrent Watch calls do not share state.
type Sampler struct {
	fetcher            Fetcher
	calculator         *sqlRateCalculator
	lastRates          obmodel.SQLRates
	lastRatesSuccessAt time.Time
	lastSampledAt      time.Time
}

func NewSampler(fetcher Fetcher) *Sampler {
	return &Sampler{
		fetcher:    fetcher,
		calculator: newSQLRateCalculator(),
	}
}

// SampleOptions controls filtering/sorting applied to a snapshot.
type SampleOptions struct {
	Filters    obmodel.Filters
	ActiveOnly bool
	SortField  obmodel.SortField
}

// Sample fetches processes and counters once and returns a Snapshot.
// Metrics failure does not block session data: prior rates are retained as stale.
func (s *Sampler) Sample(ctx context.Context, opts SampleOptions) obmodel.Snapshot {
	rows, sessionsErr := s.fetcher.FetchProcesses(ctx)
	counters, metricsErr := s.fetcher.FetchSQLCounters(ctx)
	sampledAt := time.Now()

	ratesWarmingUp := false
	ratesStale := false
	if metricsErr == nil {
		rates := s.calculator.Update(counters, sampledAt)
		if !rates.Ready {
			ratesWarmingUp = true
		}
		s.lastRates = rates
		s.lastRatesSuccessAt = sampledAt
	} else {
		ratesStale = s.lastRates.Ready
		ratesWarmingUp = !s.lastRates.Ready && s.lastRatesSuccessAt.IsZero()
	}

	var actualInterval time.Duration
	if !s.lastSampledAt.IsZero() {
		actualInterval = sampledAt.Sub(s.lastSampledAt)
	}
	s.lastSampledAt = sampledAt

	snapshot := obmodel.Snapshot{
		Rows:               rows,
		SQLRates:           obmodel.CloneSQLRates(s.lastRates),
		SampledAt:          sampledAt,
		ActualInterval:     actualInterval,
		Filters:            opts.Filters,
		ActiveOnly:         opts.ActiveOnly,
		SortField:          opts.SortField,
		SessionsErr:        sessionsErr,
		MetricsErr:         metricsErr,
		RatesWarmingUp:     ratesWarmingUp,
		RatesStale:         ratesStale,
		LastRatesSuccessAt: s.lastRatesSuccessAt,
	}

	if sessionsErr == nil {
		snapshot.Filtered = applyView(rows, opts)
		snapshot.UserStats = obmodel.SessionStatsByUser(rows)
	}

	return snapshot
}

func applyView(rows []obmodel.Process, opts SampleOptions) []obmodel.Process {
	view := rows
	if opts.ActiveOnly {
		view = obmodel.ActiveProcesses(view)
	}
	view = obmodel.ApplyFilters(view, opts.Filters)
	if opts.SortField != "" {
		obmodel.SortProcesses(view, opts.SortField)
	}
	return view
}

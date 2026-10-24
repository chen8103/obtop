package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

func TestSamplerDistinguishesWarmingUpStaleAndSessionErrors(t *testing.T) {
	fetcher := &monitorTestFetcher{
		rows: []obmodel.Process{{SessID: 1}},
		counters: []obmodel.SQLCounter{
			{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 100},
		},
	}
	sampler := NewSampler(fetcher)

	first := sampler.Sample(context.Background(), SampleOptions{})
	if !first.RatesWarmingUp {
		t.Fatalf("first sample must be warming up")
	}
	if first.RatesStale {
		t.Fatalf("first sample must not be stale")
	}

	fetcher.counters[0].Value = 110
	second := sampler.Sample(context.Background(), SampleOptions{})
	if second.RatesWarmingUp || !second.SQLRates.Ready {
		t.Fatalf("second sample must publish ready rates")
	}
	if second.ActualInterval <= 0 {
		t.Fatalf("actual interval must be tracked after first sample")
	}

	fetcher.metricsErr = fmt.Errorf("metrics unavailable")
	third := sampler.Sample(context.Background(), SampleOptions{})
	if !third.RatesStale {
		t.Fatalf("metrics failure must mark rates stale")
	}
	if !third.LastRatesSuccessAt.Equal(second.SampledAt) {
		t.Fatalf("last success time must be retained")
	}
	if _, ok := third.SQLRates.Rate(obmodel.StatSQLSelect); !ok {
		t.Fatalf("stale rates must retain last valid values")
	}

	fetcher.metricsErr = nil
	fetcher.processErr = fmt.Errorf("sessions unavailable")
	fourth := sampler.Sample(context.Background(), SampleOptions{ActiveOnly: true})
	if fourth.SessionsErr == nil {
		t.Fatalf("session error must be surfaced")
	}
	if fourth.Filtered != nil {
		t.Fatalf("filtered view must be empty when sessions fail")
	}
}

func TestSamplerAppliesActiveFilterAndSort(t *testing.T) {
	fetcher := &monitorTestFetcher{
		rows: []obmodel.Process{
			{SessID: 1, User: "a", Command: "Sleep", Time: 5},
			{SessID: 2, User: "b", Command: "Query", Time: 1, SQL: "select 1"},
			{SessID: 3, User: "c", Command: "Query", Time: 9, SQL: "select 2"},
		},
	}
	sampler := NewSampler(fetcher)
	snapshot := sampler.Sample(context.Background(), SampleOptions{
		ActiveOnly: true,
		SortField:  obmodel.SortByTime,
	})
	if len(snapshot.Filtered) != 2 {
		t.Fatalf("active filter kept %d rows, want 2", len(snapshot.Filtered))
	}
	if snapshot.Filtered[0].SessID != 3 {
		t.Fatalf("expected highest TIME first, got %d", snapshot.Filtered[0].SessID)
	}
	if len(snapshot.UserStats) != 3 {
		t.Fatalf("user stats must use raw rows, got %d", len(snapshot.UserStats))
	}
}

func TestIndependentSamplersHaveIndependentBaselines(t *testing.T) {
	fetcher := &monitorTestFetcher{
		counters: []obmodel.SQLCounter{
			{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 100},
		},
	}
	a := NewSampler(fetcher)
	b := NewSampler(fetcher)
	started := time.Now()
	_ = started

	firstA := a.Sample(context.Background(), SampleOptions{})
	firstB := b.Sample(context.Background(), SampleOptions{})
	if !firstA.RatesWarmingUp || !firstB.RatesWarmingUp {
		t.Fatalf("each sampler must warm up independently")
	}

	fetcher.counters[0].Value = 120
	secondA := a.Sample(context.Background(), SampleOptions{})
	if !secondA.SQLRates.Ready {
		t.Fatalf("sampler A must become ready on second sample")
	}
	secondB := b.Sample(context.Background(), SampleOptions{})
	if !secondB.SQLRates.Ready {
		t.Fatalf("sampler B must become ready on its own second sample")
	}
}

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

type monitorTestFetcher struct {
	rows       []obmodel.Process
	counters   []obmodel.SQLCounter
	processErr error
	metricsErr error
}

func (f *monitorTestFetcher) FetchProcesses(context.Context) ([]obmodel.Process, error) {
	return f.rows, f.processErr
}

func (f *monitorTestFetcher) FetchSQLCounters(context.Context) ([]obmodel.SQLCounter, error) {
	return f.counters, f.metricsErr
}

func TestMonitorPublishesRatesAndKeepsLastValuesWhenMetricsFail(t *testing.T) {
	fetcher := &monitorTestFetcher{
		rows: []obmodel.Process{{SessID: 1}},
		counters: []obmodel.SQLCounter{
			{ServerIP: "10.0.0.1", ServerPort: 2882, StatID: obmodel.StatSQLSelect, Value: 100},
		},
	}
	monitor := NewMonitor(fetcher, time.Second)
	monitor.fetchAndPublish(context.Background())
	first := <-monitor.Updates()
	if first.SQLRates.Ready {
		t.Fatalf("first counter sample must be warming up")
	}

	fetcher.counters[0].Value = 110
	monitor.fetchAndPublish(context.Background())
	second := <-monitor.Updates()
	if _, ok := second.SQLRates.Rate(obmodel.StatSQLSelect); !ok {
		t.Fatalf("second counter sample must publish SELECT rate")
	}

	fetcher.metricsErr = fmt.Errorf("metrics unavailable")
	monitor.fetchAndPublish(context.Background())
	third := <-monitor.Updates()
	if third.MetricsErr == nil {
		t.Fatalf("metrics error must be published independently")
	}
	if _, ok := third.SQLRates.Rate(obmodel.StatSQLSelect); !ok {
		t.Fatalf("last valid rates must be retained while metrics are stale")
	}
	if third.Err != nil {
		t.Fatalf("metrics failure must not mark process data as failed: %v", third.Err)
	}
}

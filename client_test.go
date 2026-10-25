package obtop

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chen8103/obtop/internal/service"
	"github.com/chen8103/obtop/obmodel"
)

type fakeFetcher struct {
	rows       []obmodel.Process
	counters   []obmodel.SQLCounter
	processErr error
	metricsErr error
	calls      int
}

func (f *fakeFetcher) FetchProcesses(context.Context) ([]obmodel.Process, error) {
	f.calls++
	return f.rows, f.processErr
}

func (f *fakeFetcher) FetchSQLCounters(context.Context) ([]obmodel.SQLCounter, error) {
	return f.counters, f.metricsErr
}

func TestWatchLoopStopsOnConsumeError(t *testing.T) {
	fetcher := &fakeFetcher{
		rows: []obmodel.Process{{SessID: 1, Command: "Query", SQL: "select 1"}},
	}
	sampler := service.NewSampler(fetcher)
	errStop := errors.New("stop")
	count := 0
	err := watchLoop(context.Background(), sampler, WatchOptions{Interval: 10 * time.Millisecond}, func(Snapshot) error {
		count++
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("err = %v, want %v", err, errStop)
	}
	if count != 1 {
		t.Fatalf("consume calls = %d, want 1", count)
	}
}

func TestWatchLoopHonoursCancel(t *testing.T) {
	fetcher := &fakeFetcher{rows: []obmodel.Process{{SessID: 1}}}
	sampler := service.NewSampler(fetcher)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := watchLoop(ctx, sampler, WatchOptions{Interval: 5 * time.Millisecond}, func(Snapshot) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
}

func TestWatchLoopDurationEndsCleanly(t *testing.T) {
	fetcher := &fakeFetcher{rows: []obmodel.Process{{SessID: 1}}}
	sampler := service.NewSampler(fetcher)
	err := watchLoop(context.Background(), sampler, WatchOptions{
		Interval: 5 * time.Millisecond,
		Duration: 25 * time.Millisecond,
	}, func(Snapshot) error { return nil })
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fetcher.calls < 2 {
		t.Fatalf("expected multiple samples, got %d", fetcher.calls)
	}
}

func TestWatchLoopPropagatesParentDeadline(t *testing.T) {
	fetcher := &fakeFetcher{rows: []obmodel.Process{{SessID: 1}}}
	sampler := service.NewSampler(fetcher)
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := watchLoop(parent, sampler, WatchOptions{
		Interval: time.Hour,
		Duration: time.Second,
	}, func(Snapshot) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want parent deadline exceeded", err)
	}
}

func TestWatchRecordsActualIntervalDrift(t *testing.T) {
	fetcher := &fakeFetcher{rows: []obmodel.Process{{SessID: 1}}}
	sampler := service.NewSampler(fetcher)
	var intervals []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := watchLoop(ctx, sampler, WatchOptions{Interval: 5 * time.Millisecond}, func(snap Snapshot) error {
		intervals = append(intervals, snap.ActualInterval)
		if len(intervals) >= 3 {
			cancel()
		}
		time.Sleep(15 * time.Millisecond) // slow consumer
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if intervals[0] != 0 {
		t.Fatalf("first interval should be 0, got %s", intervals[0])
	}
	if intervals[2] < 10*time.Millisecond {
		t.Fatalf("slow consumer should stretch actual interval, got %s", intervals[2])
	}
}

func TestAsDBErrorWrapsMessage(t *testing.T) {
	err := AsDBError("snapshot", fmt.Errorf("boom"))
	if err.Op != "snapshot" || err.Message != "boom" {
		t.Fatalf("unexpected %#v", err)
	}
}

func TestWatchDurationExpiresWhileConsumerRuns(t *testing.T) {
	// Leave both the next tick and the duration deadline ready. Selecting the
	// tick must not turn our own deadline into an error.
	for i := 0; i < 40; i++ {
		sampler := service.NewSampler(&fakeFetcher{})
		err := watchLoop(context.Background(), sampler, WatchOptions{
			Interval: time.Millisecond,
			Duration: 2 * time.Millisecond,
		}, func(Snapshot) error {
			time.Sleep(5 * time.Millisecond)
			return nil
		})
		if err != nil {
			t.Fatalf("iteration %d: duration should end cleanly: %v", i, err)
		}
	}
}

func TestWatchPreservesConsumerErrorAfterDuration(t *testing.T) {
	sampler := service.NewSampler(&fakeFetcher{})
	errStop := errors.New("consumer failed")
	err := watchLoop(context.Background(), sampler, WatchOptions{
		Interval: time.Millisecond,
		Duration: 10 * time.Millisecond,
	}, func(Snapshot) error {
		time.Sleep(20 * time.Millisecond)
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("err = %v, want consumer error", err)
	}
}

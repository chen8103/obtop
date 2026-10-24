package obclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

func TestBatchKillConcurrencyIsBounded(t *testing.T) {
	if maxConcurrentSessionKills > 32 {
		t.Fatalf("batch kill concurrency = %d, want at most 32", maxConcurrentSessionKills)
	}
}

func TestRunBatchSessionKillsStopsWhenProbeFails(t *testing.T) {
	var mu sync.Mutex
	var calls []int64

	result := runBatchSessionKills(context.Background(), []int64{101, 102, 103}, 3, func(ctx context.Context, sessionID int64) error {
		mu.Lock()
		calls = append(calls, sessionID)
		mu.Unlock()
		return fmt.Errorf("unknown thread id")
	})

	if len(calls) != 1 || calls[0] != 101 {
		t.Fatalf("kill calls = %#v, want only probe session 101", calls)
	}
	if result.Requested != 3 || result.Succeeded != 0 || len(result.Failures) != 1 {
		t.Fatalf("unexpected stopped batch result: %#v", result)
	}
	if got := result.Failures[0].Err.Error(); !strings.Contains(got, "batch stopped before 2 remaining sessions") {
		t.Fatalf("probe failure does not explain that the remaining sessions were not attempted: %q", got)
	}
}

func TestRunBatchSessionKillsContinuesWhenProbeSessionIsAlreadyGone(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[int64]int)

	result := runBatchSessionKills(context.Background(), []int64{101, 102, 103}, 3, func(ctx context.Context, sessionID int64) error {
		mu.Lock()
		calls[sessionID]++
		mu.Unlock()
		if sessionID == 101 {
			return fmt.Errorf(
				"kill session %d: %w",
				sessionID,
				&mysql.MySQLError{Number: 1094, Message: "Unknown thread id: 101"},
			)
		}
		return nil
	})

	if result.Requested != 3 || result.Succeeded != 3 || len(result.Failures) != 0 {
		t.Fatalf("already-gone session must not fail or stop the batch: %#v", result)
	}
	if calls[101] != 1 || calls[102] != 1 || calls[103] != 1 {
		t.Fatalf("kill calls = %#v, want every session attempted once", calls)
	}
}

func TestRunBatchSessionKillsProbesThenStartsRemainingTargetsConcurrently(t *testing.T) {
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	remainingStarted := make(chan int64, 2)
	releaseRemaining := make(chan struct{})
	done := make(chan BatchKillResult, 1)

	go func() {
		done <- runBatchSessionKills(context.Background(), []int64{101, 102, 103}, 3, func(ctx context.Context, sessionID int64) error {
			if sessionID == 101 {
				close(probeStarted)
				<-releaseProbe
				return nil
			}
			remainingStarted <- sessionID
			<-releaseRemaining
			return nil
		})
	}()

	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		t.Fatal("probe session did not start")
	}
	select {
	case sessionID := <-remainingStarted:
		t.Fatalf("session %d started before probe succeeded", sessionID)
	default:
	}
	close(releaseProbe)

	for index := 0; index < 2; index++ {
		select {
		case <-remainingStarted:
		case <-time.After(time.Second):
			t.Fatalf("only %d of 2 remaining session kills started concurrently", index)
		}
	}
	close(releaseRemaining)

	result := <-done
	if result.Requested != 3 || result.Succeeded != 3 || len(result.Failures) != 0 {
		t.Fatalf("unexpected batch result: %#v", result)
	}
}

func TestRunBatchSessionKillsDeduplicatesSessionIDs(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[int64]int)

	result := runBatchSessionKills(context.Background(), []int64{101, 101, 102, 0, -1}, 4, func(ctx context.Context, sessionID int64) error {
		mu.Lock()
		calls[sessionID]++
		mu.Unlock()
		return nil
	})

	if result.Requested != 2 || result.Succeeded != 2 {
		t.Fatalf("unexpected batch result: %#v", result)
	}
	if calls[101] != 1 || calls[102] != 1 || len(calls) != 2 {
		t.Fatalf("session kill calls were not deduplicated: %#v", calls)
	}
}

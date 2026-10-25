package obclient

import (
	"context"
	"errors"
	"fmt"
	"sync"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/chen8103/obtop/obmodel"
)

const maxConcurrentSessionKills = 32

type BatchKillResult = obmodel.BatchKillResult

type sessionKillOutcome struct {
	sessionID int64
	err       error
}

func runBatchSessionKills(
	ctx context.Context,
	sessionIDs []int64,
	concurrency int,
	kill func(context.Context, int64) error,
) BatchKillResult {
	uniqueIDs := uniquePositiveSessionIDs(sessionIDs)
	result := BatchKillResult{Requested: len(uniqueIDs)}
	if len(uniqueIDs) == 0 {
		return result
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(uniqueIDs) {
		concurrency = len(uniqueIDs)
	}

	remainingStart := 0
	for remainingStart < len(uniqueIDs) {
		probeID := uniqueIDs[remainingStart]
		err := kill(ctx, probeID)
		remainingStart++
		if err == nil || isSessionAlreadyGone(err) {
			result.Succeeded++
			if isSessionAlreadyGone(err) {
				result.AlreadyGone++
			}
			if err == nil {
				break
			}
			continue
		}
		result.Failures = append(result.Failures, obmodel.SessionKillFailure{
			SessionID: probeID,
			Err:       fmt.Errorf("probe failed; batch stopped before %d remaining sessions: %w", len(uniqueIDs)-remainingStart, err),
		})
		return result
	}
	if remainingStart == len(uniqueIDs) {
		return result
	}

	remainingIDs := uniqueIDs[remainingStart:]
	if concurrency > len(remainingIDs) {
		concurrency = len(remainingIDs)
	}
	jobs := make(chan int64, len(remainingIDs))
	outcomes := make(chan sessionKillOutcome, len(remainingIDs))
	for _, sessionID := range remainingIDs {
		jobs <- sessionID
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(concurrency)
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer workers.Done()
			for sessionID := range jobs {
				outcomes <- sessionKillOutcome{
					sessionID: sessionID,
					err:       kill(ctx, sessionID),
				}
			}
		}()
	}
	workers.Wait()
	close(outcomes)

	for outcome := range outcomes {
		if outcome.err == nil || isSessionAlreadyGone(outcome.err) {
			result.Succeeded++
			if isSessionAlreadyGone(outcome.err) {
				result.AlreadyGone++
			}
			continue
		}
		result.Failures = append(result.Failures, obmodel.SessionKillFailure{
			SessionID: outcome.sessionID,
			Err:       outcome.err,
		})
	}
	return result
}

func isSessionAlreadyGone(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1094
}

func uniquePositiveSessionIDs(sessionIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(sessionIDs))
	unique := make([]int64, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID <= 0 {
			continue
		}
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		unique = append(unique, sessionID)
	}
	return unique
}

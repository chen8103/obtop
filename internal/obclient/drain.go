package obclient

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/chen8103/obtop/obmodel"
)

const (
	maxSessionDrainRounds  = 5
	maxSessionDrainTargets = 5000
	sessionDrainInterval   = time.Second
)

type drainTargets struct {
	proxyConnectionIDs  []int64
	directConnectionIDs []int64
}

type sessionDrainOps struct {
	fetch          func(context.Context, string) ([]obmodel.Process, error)
	killProxy      func(context.Context, int64) error
	killConnection func(context.Context, int64) error
	wait           func(context.Context, time.Duration) error
}

func planSessionDrainTargets(rows []obmodel.Process) drainTargets {
	seenProxy := make(map[int64]struct{})
	seenDirect := make(map[int64]struct{})
	var targets drainTargets
	for _, row := range rows {
		if row.SessID <= 0 {
			continue
		}
		if row.ProxySessID > 0 {
			if _, exists := seenProxy[row.ProxySessID]; exists {
				continue
			}
			seenProxy[row.ProxySessID] = struct{}{}
			targets.proxyConnectionIDs = append(targets.proxyConnectionIDs, row.SessID)
			continue
		}
		if _, exists := seenDirect[row.SessID]; exists {
			continue
		}
		seenDirect[row.SessID] = struct{}{}
		targets.directConnectionIDs = append(targets.directConnectionIDs, row.SessID)
	}
	return targets
}

func runUserSessionDrain(
	ctx context.Context,
	user string,
	ops sessionDrainOps,
	report func(obmodel.SessionDrainProgress),
) (obmodel.SessionDrainResult, error) {
	user = strings.TrimSpace(user)
	result := obmodel.SessionDrainResult{User: user}
	if user == "" {
		result.StopReason = obmodel.SessionDrainFatalError
		return result, fmt.Errorf("drain user must not be empty")
	}
	if reason, stopped := sessionDrainContextStop(ctx); stopped {
		result.StopReason = reason
		return result, nil
	}
	if ops.fetch == nil || ops.killProxy == nil || ops.killConnection == nil || ops.wait == nil {
		result.StopReason = obmodel.SessionDrainFatalError
		return result, fmt.Errorf("session drain operations are incomplete")
	}

	current, err := fetchExactUserSessions(ctx, user, ops.fetch)
	if err != nil {
		return stopSessionDrain(result, ctx, err)
	}
	result.Initial = len(current)
	result.Remaining = len(current)
	seenProxySessions := proxySessionSet(current)
	seenCommandTargets := make(map[string]struct{})

	if len(current) == 0 {
		if err := ops.wait(ctx, sessionDrainInterval); err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		confirmation, err := fetchExactUserSessions(ctx, user, ops.fetch)
		if err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		trackSessionDrainChanges(current, confirmation, seenProxySessions, &result)
		current = confirmation
		result.Remaining = len(current)
		if len(current) == 0 {
			result.StopReason = obmodel.SessionDrainCompleted
			return result, nil
		}
	}

	for round := 1; round <= maxSessionDrainRounds; round++ {
		if reason, stopped := sessionDrainContextStop(ctx); stopped {
			result.StopReason = reason
			return result, nil
		}

		result.Rounds = round
		targets := planSessionDrainTargets(current)
		if result.Degraded {
			targets.directConnectionIDs = uniquePositiveSessionIDs(append(
				targets.directConnectionIDs,
				targets.proxyConnectionIDs...,
			))
			targets.proxyConnectionIDs = nil
		}
		if report != nil {
			report(obmodel.SessionDrainProgress{
				Round:         round,
				MaxRounds:     maxSessionDrainRounds,
				Initial:       result.Initial,
				Current:       len(current),
				ProxyTargets:  len(targets.proxyConnectionIDs),
				DirectTargets: len(targets.directConnectionIDs),
			})
		}
		if !reserveDrainTargetGroups(
			seenCommandTargets,
			targets.proxyConnectionIDs,
			targets.directConnectionIDs,
		) {
			result.StopReason = obmodel.SessionDrainTargetLimit
			result.Remaining = len(current)
			return result, nil
		}

		if len(targets.proxyConnectionIDs) > 0 {
			batch := runBatchSessionKills(
				ctx,
				targets.proxyConnectionIDs,
				maxConcurrentSessionKills,
				ops.killProxy,
			)
			result.ProxyCommands += batch.Succeeded + len(batch.Failures)
			result.AlreadyGone += batch.AlreadyGone
			appendDrainFailures(&result, "KILL PROXYSESSION", batch.Failures)
			if fatalErr := firstFatalBatchError(batch); fatalErr != nil {
				result.StopReason = obmodel.SessionDrainFatalError
				result.Remaining = len(current)
				return result, fatalErr
			}
			if len(batch.Failures) > 0 && isProxyCapabilityError(batch.Failures[0].Err) {
				result.Degraded = true
			}
		}

		if len(targets.directConnectionIDs) > 0 {
			batch := runBatchSessionKills(
				ctx,
				targets.directConnectionIDs,
				maxConcurrentSessionKills,
				ops.killConnection,
			)
			result.ConnectionCommands += batch.Succeeded + len(batch.Failures)
			result.AlreadyGone += batch.AlreadyGone
			appendDrainFailures(&result, "KILL CONNECTION", batch.Failures)
			if fatalErr := firstFatalBatchError(batch); fatalErr != nil {
				result.StopReason = obmodel.SessionDrainFatalError
				result.Remaining = len(current)
				return result, fatalErr
			}
		}

		if err := ops.wait(ctx, sessionDrainInterval); err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		next, err := fetchExactUserSessions(ctx, user, ops.fetch)
		if err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		trackSessionDrainChanges(current, next, seenProxySessions, &result)
		current = next
		result.Remaining = len(current)
		if len(current) != 0 {
			continue
		}

		if err := ops.wait(ctx, sessionDrainInterval); err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		confirmation, err := fetchExactUserSessions(ctx, user, ops.fetch)
		if err != nil {
			return stopSessionDrain(result, ctx, err)
		}
		trackSessionDrainChanges(current, confirmation, seenProxySessions, &result)
		current = confirmation
		result.Remaining = len(current)
		if len(current) == 0 {
			result.StopReason = obmodel.SessionDrainCompleted
			return result, nil
		}
	}

	result.StopReason = obmodel.SessionDrainRoundLimit
	result.Remaining = len(current)
	return result, nil
}

func fetchExactUserSessions(
	ctx context.Context,
	user string,
	fetch func(context.Context, string) ([]obmodel.Process, error),
) ([]obmodel.Process, error) {
	rows, err := fetch(ctx, user)
	if err != nil {
		return nil, err
	}
	exact := make([]obmodel.Process, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.User) == user && row.SessID > 0 {
			exact = append(exact, row)
		}
	}
	return exact, nil
}

func reserveDrainTargetGroups(
	seen map[string]struct{},
	proxyConnectionIDs []int64,
	directConnectionIDs []int64,
) bool {
	newKeys := make([]string, 0, len(proxyConnectionIDs)+len(directConnectionIDs))
	appendKeys := func(command string, sessionIDs []int64) {
		for _, sessionID := range uniquePositiveSessionIDs(sessionIDs) {
			key := fmt.Sprintf("%s:%d", command, sessionID)
			if _, exists := seen[key]; exists {
				continue
			}
			newKeys = append(newKeys, key)
		}
	}
	appendKeys("proxy", proxyConnectionIDs)
	appendKeys("connection", directConnectionIDs)
	if len(seen)+len(newKeys) > maxSessionDrainTargets {
		return false
	}
	for _, key := range newKeys {
		seen[key] = struct{}{}
	}
	return true
}

func appendDrainFailures(
	result *obmodel.SessionDrainResult,
	command string,
	failures []obmodel.SessionKillFailure,
) {
	for _, failure := range failures {
		result.Failures = append(result.Failures, obmodel.SessionDrainFailure{
			Command:   command,
			SessionID: failure.SessionID,
			Err:       failure.Err,
		})
	}
}

func firstFatalBatchError(batch obmodel.BatchKillResult) error {
	for _, failure := range batch.Failures {
		if isFatalSessionKillError(failure.Err) {
			return failure.Err
		}
	}
	return nil
}

func isProxyCapabilityError(err error) bool {
	if isUnsupportedProxySessionCommand(err) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1044, 1045, 1064, 1227:
		return true
	default:
		return false
	}
}

func isUnsupportedProxySessionCommand(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 5010 {
		return false
	}
	message := strings.ToLower(mysqlErr.Message)
	return strings.Contains(message, "unknown operator") ||
		strings.Contains(message, "bad internal cmd")
}

func isLegacyProxySessionKillAccepted(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) &&
		mysqlErr.Number == 1317 &&
		mysqlErr.SQLState == [5]byte{'7', '0', '1', '0', '0'}
}

func isFatalSessionKillError(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) &&
		(mysqlErr.Number == 2006 || mysqlErr.Number == 2013)
}

func sessionDrainContextStop(ctx context.Context) (obmodel.SessionDrainStopReason, bool) {
	switch ctx.Err() {
	case context.Canceled:
		return obmodel.SessionDrainCancelled, true
	case context.DeadlineExceeded:
		return obmodel.SessionDrainTimeout, true
	default:
		return "", false
	}
}

func stopSessionDrain(
	result obmodel.SessionDrainResult,
	ctx context.Context,
	err error,
) (obmodel.SessionDrainResult, error) {
	if reason, stopped := sessionDrainContextStop(ctx); stopped {
		result.StopReason = reason
		return result, nil
	}
	if errors.Is(err, context.Canceled) {
		result.StopReason = obmodel.SessionDrainCancelled
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.StopReason = obmodel.SessionDrainTimeout
		return result, nil
	}
	result.StopReason = obmodel.SessionDrainFatalError
	return result, err
}

func proxySessionSet(rows []obmodel.Process) map[int64]struct{} {
	set := make(map[int64]struct{})
	for _, row := range rows {
		if row.ProxySessID > 0 {
			set[row.ProxySessID] = struct{}{}
		}
	}
	return set
}

func trackSessionDrainChanges(
	previous []obmodel.Process,
	current []obmodel.Process,
	seenProxySessions map[int64]struct{},
	result *obmodel.SessionDrainResult,
) {
	previousByProxy := sessionIDsByProxy(previous)
	currentByProxy := sessionIDsByProxy(current)
	for proxySessID, currentIDs := range currentByProxy {
		if _, seen := seenProxySessions[proxySessID]; !seen {
			result.NewProxySessions++
			seenProxySessions[proxySessID] = struct{}{}
		}
		previousIDs, existed := previousByProxy[proxySessID]
		if existed && !sameSessionIDSet(previousIDs, currentIDs) {
			result.RebuiltIDs++
		}
	}
}

func sessionIDsByProxy(rows []obmodel.Process) map[int64]map[int64]struct{} {
	result := make(map[int64]map[int64]struct{})
	for _, row := range rows {
		if row.ProxySessID <= 0 || row.SessID <= 0 {
			continue
		}
		if result[row.ProxySessID] == nil {
			result[row.ProxySessID] = make(map[int64]struct{})
		}
		result[row.ProxySessID][row.SessID] = struct{}{}
	}
	return result
}

func sameSessionIDSet(left map[int64]struct{}, right map[int64]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for sessionID := range left {
		if _, exists := right[sessionID]; !exists {
			return false
		}
	}
	return true
}

func waitForDrain(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

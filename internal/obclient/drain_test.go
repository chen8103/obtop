package obclient

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/chen8103/obtop/obmodel"
)

func TestPlanSessionDrainTargetsGroupsProxySessions(t *testing.T) {
	rows := []obmodel.Process{
		{SessID: 101, ProxySessID: 9001, User: "app"},
		{SessID: 102, ProxySessID: 9001, User: "app"},
		{SessID: 201, User: "app"},
		{SessID: 201, User: "app"},
		{SessID: 0, ProxySessID: 9002, User: "app"},
	}

	got := planSessionDrainTargets(rows)
	if !reflect.DeepEqual(got.proxyConnectionIDs, []int64{101}) {
		t.Fatalf("proxy targets = %#v, want [101]", got.proxyConnectionIDs)
	}
	if !reflect.DeepEqual(got.directConnectionIDs, []int64{201}) {
		t.Fatalf("direct targets = %#v, want [201]", got.directConnectionIDs)
	}
}

func TestSessionKillStatementsUseConnectionID(t *testing.T) {
	if got := proxySessionKillStatement(101); got != "KILL PROXYSESSION 101" {
		t.Fatalf("proxy statement = %q, want KILL PROXYSESSION 101", got)
	}
	if got := connectionKillStatement(202); got != "KILL CONNECTION 202" {
		t.Fatalf("connection statement = %q, want KILL CONNECTION 202", got)
	}
	if got := proxySessionKillStatement(0); got != "" {
		t.Fatalf("zero proxy session statement = %q, want empty", got)
	}
	if got := connectionKillStatement(-1); got != "" {
		t.Fatalf("negative connection statement = %q, want empty", got)
	}
}

func TestCompatibleProxySessionKillFallsBackToLegacySyntaxOn5010(t *testing.T) {
	var legacyMode uint32
	var statements []string
	exec := func(ctx context.Context, statement string, sessionID int64) error {
		statements = append(statements, statement)
		if statement == "KILL PROXYSESSION 101" {
			return fmt.Errorf(
				"%s failed: %w",
				statement,
				&mysql.MySQLError{
					Number:  5010,
					Message: "Unknown operator, bad internal cmd",
				},
			)
		}
		return nil
	}

	if err := executeCompatibleProxySessionKill(
		context.Background(),
		101,
		&legacyMode,
		exec,
	); err != nil {
		t.Fatal(err)
	}
	if err := executeCompatibleProxySessionKill(
		context.Background(),
		102,
		&legacyMode,
		exec,
	); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"KILL PROXYSESSION 101",
		"KILL 101",
		"KILL 102",
	}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("statements = %#v, want %#v", statements, want)
	}
}

func TestCompatibleProxySessionKillTreatsLegacy1317AsAccepted(t *testing.T) {
	var legacyMode uint32
	var statements []string
	exec := func(ctx context.Context, statement string, sessionID int64) error {
		statements = append(statements, statement)
		switch statement {
		case "KILL PROXYSESSION 101":
			return fmt.Errorf(
				"%s failed: %w",
				statement,
				&mysql.MySQLError{
					Number:  5010,
					Message: "Unknown operator, bad internal cmd",
				},
			)
		case "KILL 101", "KILL 102":
			return fmt.Errorf(
				"%s failed: %w",
				statement,
				&mysql.MySQLError{
					Number:   1317,
					SQLState: [5]byte{'7', '0', '1', '0', '0'},
					Message:  "Query execution was interrupted",
				},
			)
		default:
			return nil
		}
	}

	if err := executeCompatibleProxySessionKill(
		context.Background(),
		101,
		&legacyMode,
		exec,
	); err != nil {
		t.Fatalf("first legacy kill = %v, want accepted", err)
	}
	if err := executeCompatibleProxySessionKill(
		context.Background(),
		102,
		&legacyMode,
		exec,
	); err != nil {
		t.Fatalf("second legacy kill = %v, want accepted", err)
	}

	want := []string{
		"KILL PROXYSESSION 101",
		"KILL 101",
		"KILL 102",
	}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("statements = %#v, want %#v", statements, want)
	}
}

func TestRunUserSessionDrainRequiresTwoEmptySnapshots(t *testing.T) {
	fetch := snapshotFetcher(
		[]obmodel.Process{{SessID: 101, User: "app"}},
		nil,
		nil,
	)
	fetches := 0
	ops := sessionDrainOps{
		fetch: func(ctx context.Context, user string) ([]obmodel.Process, error) {
			fetches++
			return fetch(ctx, user)
		},
		killConnection: func(context.Context, int64) error { return nil },
		killProxy:      func(context.Context, int64) error { return nil },
		wait:           noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != obmodel.SessionDrainCompleted || result.Remaining != 0 {
		t.Fatalf("result = %#v, want completed with zero remaining", result)
	}
	if fetches != 3 {
		t.Fatalf("fetches = %d, want 3", fetches)
	}
}

func TestRunUserSessionDrainTracksRebuiltAndNewProxySessions(t *testing.T) {
	fetch := snapshotFetcher(
		[]obmodel.Process{{SessID: 101, ProxySessID: 9001, User: "app"}},
		[]obmodel.Process{
			{SessID: 102, ProxySessID: 9001, User: "app"},
			{SessID: 103, ProxySessID: 9002, User: "app"},
		},
		nil,
		nil,
	)
	var proxyCalls []int64
	ops := sessionDrainOps{
		fetch: fetch,
		killProxy: func(ctx context.Context, sessionID int64) error {
			proxyCalls = append(proxyCalls, sessionID)
			return nil
		},
		killConnection: func(context.Context, int64) error { return nil },
		wait:           noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proxyCalls, []int64{101, 102, 103}) {
		t.Fatalf("proxy calls = %#v, want [101 102 103]", proxyCalls)
	}
	if result.RebuiltIDs != 1 {
		t.Fatalf("rebuilt ids = %d, want 1", result.RebuiltIDs)
	}
	if result.NewProxySessions != 1 {
		t.Fatalf("new proxy sessions = %d, want 1", result.NewProxySessions)
	}
}

func TestRunUserSessionDrainFallsBackAfterProxyCapabilityError(t *testing.T) {
	fetch := snapshotFetcher(
		[]obmodel.Process{{SessID: 101, ProxySessID: 9001, User: "app"}},
		[]obmodel.Process{{SessID: 101, ProxySessID: 9001, User: "app"}},
		nil,
		nil,
	)
	var connectionCalls []int64
	ops := sessionDrainOps{
		fetch: fetch,
		killProxy: func(context.Context, int64) error {
			return &mysql.MySQLError{Number: 1227, Message: "access denied"}
		},
		killConnection: func(ctx context.Context, sessionID int64) error {
			connectionCalls = append(connectionCalls, sessionID)
			return nil
		},
		wait: noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded {
		t.Fatalf("result = %#v, want degraded", result)
	}
	if !reflect.DeepEqual(connectionCalls, []int64{101}) {
		t.Fatalf("connection calls = %#v, want [101]", connectionCalls)
	}
	if len(result.Failures) == 0 || result.Failures[0].Command != "KILL PROXYSESSION" {
		t.Fatalf("failures = %#v, want proxy capability failure", result.Failures)
	}
}

func TestRunUserSessionDrainCountsAlreadyGone(t *testing.T) {
	fetch := snapshotFetcher(
		[]obmodel.Process{{SessID: 201, User: "app"}},
		nil,
		nil,
	)
	ops := sessionDrainOps{
		fetch: fetch,
		killProxy: func(context.Context, int64) error {
			return nil
		},
		killConnection: func(context.Context, int64) error {
			return fmt.Errorf("kill: %w", &mysql.MySQLError{Number: 1094, Message: "unknown thread"})
		},
		wait: noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyGone != 1 {
		t.Fatalf("already gone = %d, want 1", result.AlreadyGone)
	}
}

func TestRunUserSessionDrainStopsAtRoundLimit(t *testing.T) {
	snapshot := []obmodel.Process{{SessID: 201, User: "app"}}
	ops := sessionDrainOps{
		fetch: func(context.Context, string) ([]obmodel.Process, error) {
			return snapshot, nil
		},
		killProxy:      func(context.Context, int64) error { return nil },
		killConnection: func(context.Context, int64) error { return nil },
		wait:           noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != obmodel.SessionDrainRoundLimit || result.Rounds != maxSessionDrainRounds {
		t.Fatalf("result = %#v, want round limit after %d rounds", result, maxSessionDrainRounds)
	}
	if result.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", result.Remaining)
	}
}

func TestRunUserSessionDrainStopsAtTargetLimit(t *testing.T) {
	rows := make([]obmodel.Process, maxSessionDrainTargets+1)
	for index := range rows {
		rows[index] = obmodel.Process{SessID: int64(index + 1), User: "app"}
	}
	ops := sessionDrainOps{
		fetch:          snapshotFetcher(rows),
		killProxy:      func(context.Context, int64) error { return nil },
		killConnection: func(context.Context, int64) error { return nil },
		wait:           noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != obmodel.SessionDrainTargetLimit {
		t.Fatalf("stop reason = %q, want target limit", result.StopReason)
	}
}

func TestRunUserSessionDrainReturnsFatalConnectionError(t *testing.T) {
	ops := sessionDrainOps{
		fetch:     snapshotFetcher([]obmodel.Process{{SessID: 201, User: "app"}}),
		killProxy: func(context.Context, int64) error { return nil },
		killConnection: func(context.Context, int64) error {
			return driver.ErrBadConn
		},
		wait: noDrainWait,
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("error = %v, want driver.ErrBadConn", err)
	}
	if result.StopReason != obmodel.SessionDrainFatalError {
		t.Fatalf("stop reason = %q, want fatal error", result.StopReason)
	}
}

func TestRunUserSessionDrainHonorsCancellationAndTimeout(t *testing.T) {
	tests := []struct {
		name       string
		ctx        func() context.Context
		stopReason obmodel.SessionDrainStopReason
	}{
		{
			name: "cancelled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			stopReason: obmodel.SessionDrainCancelled,
		},
		{
			name: "timeout",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				return ctx
			},
			stopReason: obmodel.SessionDrainTimeout,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetched := false
			ops := sessionDrainOps{
				fetch: func(context.Context, string) ([]obmodel.Process, error) {
					fetched = true
					return nil, nil
				},
				killProxy:      func(context.Context, int64) error { return nil },
				killConnection: func(context.Context, int64) error { return nil },
				wait:           noDrainWait,
			}

			result, err := runUserSessionDrain(test.ctx(), "app", ops, nil)
			if err != nil {
				t.Fatal(err)
			}
			if fetched {
				t.Fatal("fetch must not run after context is already done")
			}
			if result.StopReason != test.stopReason {
				t.Fatalf("stop reason = %q, want %q", result.StopReason, test.stopReason)
			}
		})
	}
}

func snapshotFetcher(snapshots ...[]obmodel.Process) func(context.Context, string) ([]obmodel.Process, error) {
	index := 0
	return func(ctx context.Context, user string) ([]obmodel.Process, error) {
		if index >= len(snapshots) {
			return nil, fmt.Errorf("unexpected fetch %d", index+1)
		}
		rows := snapshots[index]
		index++
		return rows, nil
	}
}

func noDrainWait(context.Context, time.Duration) error {
	return nil
}

package obclient

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/chen8103/obtop/internal/config"
	"github.com/chen8103/obtop/obmodel"
)

const activeSQLQuery = `
SELECT
  ID,
  PROXY_SESSID,
  SVR_IP,
  USER,
  HOST,
  DB,
  COMMAND,
  TIME,
  TOTAL_TIME,
  STATE,
  INFO
FROM oceanbase.GV$OB_PROCESSLIST
ORDER BY TIME DESC
`

const sessionSQLQuery = `
SELECT DB, INFO
FROM oceanbase.GV$OB_PROCESSLIST
WHERE ID = ?
LIMIT 1
`

const userSessionsQuery = `
SELECT ID, PROXY_SESSID, USER, HOST
FROM oceanbase.GV$OB_PROCESSLIST
WHERE USER = ?
`

const sqlCountersQuery = `
SELECT /*+ READ_CONSISTENCY(WEAK) */
  CON_ID,
  SVR_IP,
  SVR_PORT,
  STAT_ID,
  SUM(VALUE) AS VALUE
FROM oceanbase.GV$SYSSTAT
WHERE STAT_ID IN (40000, 40002, 40004, 40006, 40008, 40018, 40025, 40027)
GROUP BY CON_ID, SVR_IP, SVR_PORT, STAT_ID
`

type Client struct {
	db  *sql.DB
	dsn string
}

func New(ctx context.Context, cfg config.Config) (*Client, error) {
	dsn := cfg.DSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Second)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Client{db: db, dsn: dsn}, nil
}

func (c *Client) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *Client) FetchProcesses(ctx context.Context) ([]obmodel.Process, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := c.db.QueryContext(queryCtx, activeSQLQuery)
	if err != nil {
		return nil, fmt.Errorf("query active sql: %w", err)
	}
	defer rows.Close()

	processes := make([]obmodel.Process, 0, 32)
	for rows.Next() {
		var proc obmodel.Process
		var sessID sql.NullInt64
		var proxySessID sql.NullInt64
		var serverIP sql.NullString
		var user sql.NullString
		var host sql.NullString
		var dbName sql.NullString
		var command sql.NullString
		var state sql.NullString
		var sqlText sql.NullString
		var elapsed sql.NullFloat64
		var totalElapsed sql.NullFloat64

		if err := rows.Scan(
			&sessID,
			&proxySessID,
			&serverIP,
			&user,
			&host,
			&dbName,
			&command,
			&elapsed,
			&totalElapsed,
			&state,
			&sqlText,
		); err != nil {
			return nil, fmt.Errorf("scan processlist row: %w", err)
		}

		proc.SessID = sessID.Int64
		proc.ProxySessID = proxySessID.Int64
		proc.ServerIP = serverIP.String
		proc.User = user.String
		proc.Host = host.String
		proc.DBName = dbName.String
		proc.Command = command.String
		proc.Time = int64(elapsed.Float64)
		proc.TotalTime = int64(totalElapsed.Float64)
		proc.State = state.String
		proc.SQL = sqlText.String
		processes = append(processes, proc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate processlist rows: %w", err)
	}

	return processes, nil
}

func (c *Client) fetchUserSessions(ctx context.Context, user string) ([]obmodel.Process, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := c.db.QueryContext(queryCtx, userSessionsQuery, user)
	if err != nil {
		return nil, fmt.Errorf("query sessions for user %q: %w", user, err)
	}
	defer rows.Close()

	processes := make([]obmodel.Process, 0, 32)
	for rows.Next() {
		var sessID sql.NullInt64
		var proxySessID sql.NullInt64
		var rowUser sql.NullString
		var host sql.NullString
		if err := rows.Scan(&sessID, &proxySessID, &rowUser, &host); err != nil {
			return nil, fmt.Errorf("scan session for user %q: %w", user, err)
		}
		proc := obmodel.Process{
			SessID:      sessID.Int64,
			ProxySessID: proxySessID.Int64,
			User:        rowUser.String,
			Host:        host.String,
		}
		if strings.TrimSpace(proc.User) == user && proc.SessID > 0 {
			processes = append(processes, proc)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions for user %q: %w", user, err)
	}
	return processes, nil
}

func (c *Client) FetchSQLCounters(ctx context.Context) ([]obmodel.SQLCounter, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := c.db.QueryContext(queryCtx, sqlCountersQuery)
	if err != nil {
		return nil, fmt.Errorf("query sql counters: %w", err)
	}
	defer rows.Close()

	counters := make([]obmodel.SQLCounter, 0, len(obmodel.SQLCounterStatIDs))
	for rows.Next() {
		var tenantID sql.NullInt64
		var serverIP sql.NullString
		var serverPort sql.NullInt64
		var statID sql.NullInt64
		var value sql.NullInt64
		if err := rows.Scan(&tenantID, &serverIP, &serverPort, &statID, &value); err != nil {
			return nil, fmt.Errorf("scan sql counter row: %w", err)
		}
		counters = append(counters, obmodel.SQLCounter{
			TenantID:   tenantID.Int64,
			ServerIP:   serverIP.String,
			ServerPort: serverPort.Int64,
			StatID:     statID.Int64,
			Value:      value.Int64,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sql counter rows: %w", err)
	}
	return counters, nil
}

func (c *Client) KillSession(ctx context.Context, sessID int64) error {
	return execSessionKill(ctx, c.db, connectionKillStatement(sessID), sessID)
}

func (c *Client) DrainUserSessions(
	ctx context.Context,
	user string,
	report func(obmodel.SessionDrainProgress),
) (obmodel.SessionDrainResult, error) {
	killDB, err := sql.Open("mysql", c.dsn)
	if err != nil {
		return obmodel.SessionDrainResult{
			User:       user,
			StopReason: obmodel.SessionDrainFatalError,
		}, fmt.Errorf("open session drain connections: %w", err)
	}
	defer killDB.Close()

	killDB.SetMaxOpenConns(maxConcurrentSessionKills)
	killDB.SetMaxIdleConns(maxConcurrentSessionKills)
	killDB.SetConnMaxLifetime(30 * time.Second)

	var legacyProxyKillMode uint32
	return runUserSessionDrain(ctx, user, sessionDrainOps{
		fetch: c.fetchUserSessions,
		killProxy: func(killCtx context.Context, sessionID int64) error {
			return executeCompatibleProxySessionKill(
				killCtx,
				sessionID,
				&legacyProxyKillMode,
				func(execCtx context.Context, statement string, targetID int64) error {
					return execSessionKill(execCtx, killDB, statement, targetID)
				},
			)
		},
		killConnection: func(killCtx context.Context, sessionID int64) error {
			return execSessionKill(
				killCtx,
				killDB,
				connectionKillStatement(sessionID),
				sessionID,
			)
		},
		wait: waitForDrain,
	}, report)
}

func proxySessionKillStatement(sessionID int64) string {
	if sessionID <= 0 {
		return ""
	}
	return "KILL PROXYSESSION " + strconv.FormatInt(sessionID, 10)
}

func legacyProxySessionKillStatement(sessionID int64) string {
	if sessionID <= 0 {
		return ""
	}
	return "KILL " + strconv.FormatInt(sessionID, 10)
}

func connectionKillStatement(sessionID int64) string {
	if sessionID <= 0 {
		return ""
	}
	return "KILL CONNECTION " + strconv.FormatInt(sessionID, 10)
}

type sessionKillExecutor func(context.Context, string, int64) error

func executeCompatibleProxySessionKill(
	ctx context.Context,
	sessionID int64,
	legacyMode *uint32,
	exec sessionKillExecutor,
) error {
	if legacyMode == nil {
		return fmt.Errorf("proxy session kill mode is nil")
	}
	if atomic.LoadUint32(legacyMode) == 1 {
		return executeLegacyProxySessionKill(ctx, sessionID, exec)
	}

	err := exec(ctx, proxySessionKillStatement(sessionID), sessionID)
	if !isUnsupportedProxySessionCommand(err) {
		return err
	}

	atomic.StoreUint32(legacyMode, 1)
	return executeLegacyProxySessionKill(ctx, sessionID, exec)
}

func executeLegacyProxySessionKill(
	ctx context.Context,
	sessionID int64,
	exec sessionKillExecutor,
) error {
	err := exec(ctx, legacyProxySessionKillStatement(sessionID), sessionID)
	if isLegacyProxySessionKillAccepted(err) {
		return nil
	}
	return err
}

func execSessionKill(ctx context.Context, db *sql.DB, statement string, sessionID int64) error {
	if sessionID <= 0 || statement == "" {
		return fmt.Errorf("invalid session id %d", sessionID)
	}
	killCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(killCtx, statement); err != nil {
		return fmt.Errorf("%s failed for session %d: %w", statement, sessionID, err)
	}
	return nil
}


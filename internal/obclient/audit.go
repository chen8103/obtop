package obclient

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

var (
	// ErrAuditDisabled means SQL audit is turned off on the target tenant.
	ErrAuditDisabled = errors.New("sql audit is disabled")
	// ErrAuditNoPermission means the current user cannot query GV$OB_SQL_AUDIT.
	ErrAuditNoPermission = errors.New("no permission to query GV$OB_SQL_AUDIT")
	// ErrAuditUnsupported means the view/columns are unavailable on this version.
	ErrAuditUnsupported = errors.New("GV$OB_SQL_AUDIT is unavailable or unsupported")
)

const auditProbeQuery = `
SELECT COUNT(*) FROM oceanbase.GV$OB_SQL_AUDIT WHERE 1 = 0
`

// OceanBase exposes both a config parameter and a tenant system variable.
// SHOW VARIABLES LIKE 'enable_sql_audit' returns empty; use the names below.
const (
	auditEnableParamQuery = `SHOW PARAMETERS LIKE 'enable_sql_audit'`
	auditEnableVarQuery   = `SHOW VARIABLES LIKE 'ob_enable_sql_audit'`
)

const auditOldestQuery = `
SELECT
  SVR_IP,
  SVR_PORT,
  MIN(REQUEST_ID) AS MIN_REQUEST_ID,
  MIN(REQUEST_TIME) AS MIN_REQUEST_TIME
FROM oceanbase.GV$OB_SQL_AUDIT
GROUP BY SVR_IP, SVR_PORT
`

// AuditCapability describes whether SQL audit history can be queried.
type AuditCapability struct {
	Available    bool
	Enabled      bool
	EnabledKnown bool // false when neither parameter nor variable could be read
	Reason       string
}

// ProbeAuditCapability checks view access and SQL audit enable flags.
// Callers must treat unavailable as an error, not an empty history page.
func (c *Client) ProbeAuditCapability(ctx context.Context) (AuditCapability, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var dummy int
	if err := c.db.QueryRowContext(queryCtx, auditProbeQuery).Scan(&dummy); err != nil {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "access denied") || strings.Contains(msg, "privilege"):
			return AuditCapability{Reason: ErrAuditNoPermission.Error()}, ErrAuditNoPermission
		case strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "unknown table") ||
			strings.Contains(msg, "unknown column") || strings.Contains(msg, "not exist"):
			return AuditCapability{Reason: ErrAuditUnsupported.Error()}, ErrAuditUnsupported
		default:
			return AuditCapability{Reason: err.Error()}, fmt.Errorf("%w: %v", ErrAuditUnsupported, err)
		}
	}

	enabled, known, detail := c.readSQLAuditEnabled(queryCtx)
	if !known {
		return AuditCapability{
			Available:    true,
			Enabled:      false,
			EnabledKnown: false,
			Reason:       detail,
		}, nil
	}
	if !enabled {
		return AuditCapability{
			Available:    true,
			Enabled:      false,
			EnabledKnown: true,
			Reason:       ErrAuditDisabled.Error(),
		}, ErrAuditDisabled
	}
	return AuditCapability{Available: true, Enabled: true, EnabledKnown: true}, nil
}

// readSQLAuditEnabled checks the config parameter enable_sql_audit and the
// tenant variable ob_enable_sql_audit. Both must be known and on for enabled=true.
// Returns (enabled, known, detail).
func (c *Client) readSQLAuditEnabled(ctx context.Context) (bool, bool, string) {
	param, paramErr := c.readBoolSetting(ctx, auditEnableParamQuery, true)
	variable, varErr := c.readBoolSetting(ctx, auditEnableVarQuery, false)
	return combineAuditEnableFlags(param, paramErr, variable, varErr)
}

// boolSettingAgg aggregates multi-row SHOW PARAMETERS / VARIABLES results.
type boolSettingAgg struct {
	Found  bool
	AnyOff bool
	AllOn  bool
	Rows   int
}

func combineAuditEnableFlags(
	param boolSettingAgg, paramErr error,
	variable boolSettingAgg, varErr error,
) (enabled bool, known bool, detail string) {
	var details []string
	details = append(details, formatBoolSettingDetail("parameter enable_sql_audit", param, paramErr))
	details = append(details, formatBoolSettingDetail("variable ob_enable_sql_audit", variable, varErr))
	detail = strings.Join(details, "; ")

	paramOff := paramErr == nil && param.Found && param.AnyOff
	varOff := varErr == nil && variable.Found && variable.AnyOff
	if paramOff || varOff {
		return false, true, detail
	}

	paramKnownOn := paramErr == nil && param.Found && param.AllOn
	varKnownOn := varErr == nil && variable.Found && variable.AllOn
	if paramKnownOn && varKnownOn {
		return true, true, detail
	}

	// Either source missing, errored, or incomplete — do not claim enabled.
	return false, false, "sql audit enable status unknown: " + detail
}

func formatBoolSettingDetail(label string, agg boolSettingAgg, err error) string {
	if err != nil {
		return label + ": " + err.Error()
	}
	if !agg.Found {
		return label + " not found"
	}
	switch {
	case agg.AnyOff:
		return fmt.Sprintf("%s=false (rows=%d)", label, agg.Rows)
	case agg.AllOn:
		return fmt.Sprintf("%s=true (rows=%d)", label, agg.Rows)
	default:
		return fmt.Sprintf("%s unknown (rows=%d)", label, agg.Rows)
	}
}

// readBoolSetting runs SHOW PARAMETERS / SHOW VARIABLES style queries and
// aggregates every returned row. Any False/OFF makes AnyOff true.
func (c *Client) readBoolSetting(ctx context.Context, query string, parametersMode bool) (boolSettingAgg, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return boolSettingAgg{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return boolSettingAgg{}, err
	}
	if len(cols) == 0 {
		return boolSettingAgg{}, fmt.Errorf("no columns returned")
	}

	valIdx := 1
	if parametersMode {
		for i, col := range cols {
			if strings.EqualFold(col, "value") {
				valIdx = i
				break
			}
		}
	}

	var agg boolSettingAgg
	anyOn := false
	for rows.Next() {
		raw := make([]interface{}, len(cols))
		refs := make([]interface{}, len(cols))
		for i := range raw {
			refs[i] = &raw[i]
		}
		if err := rows.Scan(refs...); err != nil {
			return boolSettingAgg{}, err
		}
		if valIdx >= len(raw) {
			continue
		}
		name := stringifyAuditValue(raw[0])
		val := stringifyAuditValue(raw[valIdx])
		if name == "" && val == "" {
			continue
		}
		agg.Found = true
		agg.Rows++
		if parseAuditBool(val) {
			anyOn = true
		} else {
			agg.AnyOff = true
		}
	}
	if err := rows.Err(); err != nil {
		return boolSettingAgg{}, err
	}
	agg.AllOn = agg.Found && !agg.AnyOff && anyOn
	return agg, nil
}

func stringifyAuditValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func parseAuditBool(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "1" || v == "on" || v == "true" || v == "yes"
}

// QuerySQLHistory reads a page of GV$OB_SQL_AUDIT records.
// Completeness reflects the retention window (and enable-flag certainty).
// HasMore is true when this page hit the limit and more rows may remain.
func (c *Client) QuerySQLHistory(ctx context.Context, opts obmodel.HistoryOptions) (obmodel.HistoryPage, error) {
	cap, probeErr := c.ProbeAuditCapability(ctx)
	if probeErr != nil {
		return obmodel.HistoryPage{
			Completeness: obmodel.CompletenessUnknown,
			GapReason:    probeErr.Error(),
			QueriedAt:    time.Now(),
			WindowStart:  opts.Since,
			WindowEnd:    opts.Until,
		}, probeErr
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}

	until := opts.Until
	if until.IsZero() {
		until = time.Now()
	}
	since := opts.Since
	if since.IsZero() {
		since = until.Add(-5 * time.Minute)
	}

	page := obmodel.HistoryPage{
		QueriedAt:   time.Now(),
		WindowStart: since,
		WindowEnd:   until,
	}

	oldest, oldestErr := c.fetchAuditOldest(ctx)
	if oldestErr != nil {
		page.Completeness = obmodel.CompletenessUnknown
		page.GapReason = "failed to inspect audit retention window: " + oldestErr.Error()
	} else {
		page.Completeness, page.GapReason = evaluateAuditCompleteness(since, oldest)
	}

	// Enable-flag unknown must not be reported as a complete retention window.
	if !cap.EnabledKnown {
		page.Completeness = obmodel.CompletenessUnknown
		if page.GapReason == "" {
			page.GapReason = cap.Reason
		} else {
			page.GapReason = cap.Reason + "; " + page.GapReason
		}
	}

	query, args := buildAuditHistoryQuery(opts, since, until, limit)
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := c.db.QueryContext(queryCtx, query, args...)
	if err != nil {
		page.Completeness = obmodel.CompletenessUnknown
		page.GapReason = err.Error()
		return page, fmt.Errorf("query sql audit history: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	cursors := make(map[string]obmodel.HistoryCursor)
	records := make([]obmodel.HistoryRecord, 0, limit)

	for rows.Next() {
		var (
			requestID   sql.NullInt64
			serverIP    sql.NullString
			serverPort  sql.NullInt64
			tenantID    sql.NullInt64
			userName    sql.NullString
			dbName      sql.NullString
			clientIP    sql.NullString
			sqlText     sql.NullString
			requestTime sql.NullInt64
			elapsedUS   sql.NullInt64
			retCode     sql.NullInt64
			traceID     sql.NullString
		)
		if err := rows.Scan(
			&requestID, &serverIP, &serverPort, &tenantID, &userName, &dbName,
			&clientIP, &sqlText, &requestTime, &elapsedUS, &retCode, &traceID,
		); err != nil {
			return page, fmt.Errorf("scan sql audit row: %w", err)
		}

		rec := obmodel.HistoryRecord{
			RequestID:   requestID.Int64,
			ServerIP:    serverIP.String,
			ServerPort:  serverPort.Int64,
			TenantID:    tenantID.Int64,
			User:        userName.String,
			DBName:      dbName.String,
			ClientIP:    clientIP.String,
			SQL:         sqlText.String,
			RequestTime: time.Unix(0, requestTime.Int64*int64(time.Microsecond)),
			ElapsedUS:   elapsedUS.Int64,
			RetCode:     retCode.Int64,
			TraceID:     traceID.String,
		}
		rec.SourceID = formatAuditSourceID(rec.ServerIP, rec.ServerPort, rec.RequestID)

		if _, ok := seen[rec.SourceID]; ok {
			continue
		}
		seen[rec.SourceID] = struct{}{}
		records = append(records, rec)

		nodeKey := rec.ServerIP + ":" + strconv.FormatInt(rec.ServerPort, 10)
		prev, ok := cursors[nodeKey]
		if !ok || rec.RequestID > prev.RequestID {
			cursors[nodeKey] = obmodel.HistoryCursor{
				ServerIP:   rec.ServerIP,
				ServerPort: rec.ServerPort,
				RequestID:  rec.RequestID,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("iterate sql audit rows: %w", err)
	}

	page.Records = records
	page.NextCursors = make([]obmodel.HistoryCursor, 0, len(cursors))
	for _, cursor := range cursors {
		page.NextCursors = append(page.NextCursors, cursor)
	}
	// Hitting the page limit means the export may be truncated even when the
	// retention window itself looks complete.
	if len(records) >= limit {
		page.HasMore = true
		page.NextCursor = pickNextCursor(records)
	}
	return page, nil
}

func pickNextCursor(records []obmodel.HistoryRecord) *obmodel.HistoryCursor {
	if len(records) == 0 {
		return nil
	}
	last := records[len(records)-1]
	return &obmodel.HistoryCursor{
		ServerIP:   last.ServerIP,
		ServerPort: last.ServerPort,
		RequestID:  last.RequestID,
	}
}

type auditOldest struct {
	ServerIP       string
	ServerPort     int64
	MinRequestID   int64
	MinRequestTime time.Time
}

func (c *Client) fetchAuditOldest(ctx context.Context) ([]auditOldest, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := c.db.QueryContext(queryCtx, auditOldestQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]auditOldest, 0, 4)
	for rows.Next() {
		var (
			serverIP   sql.NullString
			serverPort sql.NullInt64
			minReqID   sql.NullInt64
			minReqTime sql.NullInt64
		)
		if err := rows.Scan(&serverIP, &serverPort, &minReqID, &minReqTime); err != nil {
			return nil, err
		}
		out = append(out, auditOldest{
			ServerIP:       serverIP.String,
			ServerPort:     serverPort.Int64,
			MinRequestID:   minReqID.Int64,
			MinRequestTime: time.Unix(0, minReqTime.Int64*int64(time.Microsecond)),
		})
	}
	return out, rows.Err()
}

func evaluateAuditCompleteness(since time.Time, oldest []auditOldest) (obmodel.Completeness, string) {
	if len(oldest) == 0 {
		return obmodel.CompletenessUnknown, "no audit records currently retained"
	}
	for _, node := range oldest {
		if node.MinRequestTime.After(since) {
			return obmodel.CompletenessKnownGap, fmt.Sprintf(
				"audit retention on %s:%d starts at %s, later than window start %s",
				node.ServerIP, node.ServerPort,
				node.MinRequestTime.UTC().Format(time.RFC3339Nano),
				since.UTC().Format(time.RFC3339Nano),
			)
		}
	}
	return obmodel.CompletenessComplete, ""
}

func buildAuditHistoryQuery(opts obmodel.HistoryOptions, since, until time.Time, limit int) (string, []interface{}) {
	var b strings.Builder
	b.WriteString(`
SELECT
  REQUEST_ID,
  SVR_IP,
  SVR_PORT,
  TENANT_ID,
  USER_NAME,
  DB_NAME,
  CLIENT_IP,
  QUERY_SQL,
  REQUEST_TIME,
  ELAPSED_TIME,
  RET_CODE,
  TRACE_ID
FROM oceanbase.GV$OB_SQL_AUDIT
WHERE REQUEST_TIME >= ?
  AND REQUEST_TIME < ?
`)
	args := []interface{}{
		since.UnixNano() / int64(time.Microsecond),
		until.UnixNano() / int64(time.Microsecond),
	}

	if opts.Cursor != nil && opts.Cursor.RequestID > 0 {
		b.WriteString(" AND (SVR_IP > ? OR (SVR_IP = ? AND SVR_PORT > ?) OR (SVR_IP = ? AND SVR_PORT = ? AND REQUEST_ID > ?))")
		args = append(args,
			opts.Cursor.ServerIP,
			opts.Cursor.ServerIP, opts.Cursor.ServerPort,
			opts.Cursor.ServerIP, opts.Cursor.ServerPort, opts.Cursor.RequestID,
		)
	}
	if opts.ServerIP != "" {
		b.WriteString(" AND SVR_IP = ?")
		args = append(args, opts.ServerIP)
	}
	if opts.ServerPort > 0 {
		b.WriteString(" AND SVR_PORT = ?")
		args = append(args, opts.ServerPort)
	}
	if user := strings.TrimSpace(opts.User); user != "" {
		b.WriteString(" AND USER_NAME LIKE ?")
		args = append(args, "%"+user+"%")
	}
	if db := strings.TrimSpace(opts.DBName); db != "" {
		b.WriteString(" AND DB_NAME LIKE ?")
		args = append(args, "%"+db+"%")
	}
	if keyword := strings.TrimSpace(opts.Keyword); keyword != "" {
		b.WriteString(" AND QUERY_SQL LIKE ?")
		args = append(args, "%"+keyword+"%")
	}

	b.WriteString(" ORDER BY SVR_IP, SVR_PORT, REQUEST_ID LIMIT ?")
	args = append(args, limit)
	return b.String(), args
}

func formatAuditSourceID(serverIP string, serverPort, requestID int64) string {
	return serverIP + ":" + strconv.FormatInt(serverPort, 10) + "/" + strconv.FormatInt(requestID, 10)
}

// MergeCompletenessWithEnableUnknown forces unknown when audit enable status
// could not be determined. Exported for unit tests of the completeness rule.
func MergeCompletenessWithEnableUnknown(
	completeness obmodel.Completeness,
	gapReason string,
	enabledKnown bool,
	enableReason string,
) (obmodel.Completeness, string) {
	if enabledKnown {
		return completeness, gapReason
	}
	if gapReason == "" {
		return obmodel.CompletenessUnknown, enableReason
	}
	return obmodel.CompletenessUnknown, enableReason + "; " + gapReason
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chen8103/obtop"
	"github.com/chen8103/obtop/internal/config"
)

const (
	exitOK            = 0
	exitQueryError    = 1
	exitUsage         = 2
	exitPartialFail   = 3
	exitCancelled     = 130
)

func isMachineCommand(name string) bool {
	switch name {
	case "snapshot", "watch", "sql-history", "explain":
		return true
	default:
		return false
	}
}

func runMachineCommand(args []string) int {
	if len(args) == 0 {
		writeCLIUsage(os.Stderr)
		return exitUsage
	}
	cmd := args[0]
	rest := args[1:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "snapshot":
		return runSnapshot(ctx, rest)
	case "watch":
		return runWatch(ctx, rest)
	case "sql-history":
		return runSQLHistory(ctx, rest)
	case "explain":
		return runExplain(ctx, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		writeCLIUsage(os.Stderr)
		return exitUsage
	}
}

type connFlags struct {
	host     string
	port     int
	user     string
	password string
	database string
}

func addConnFlags(fs *flag.FlagSet, c *connFlags) {
	fs.StringVar(&c.host, "h", "127.0.0.1", "OBProxy address")
	fs.IntVar(&c.port, "P", 3306, "OBProxy port")
	fs.StringVar(&c.user, "u", "", "username (user or user@tenant)")
	fs.StringVar(&c.password, "p", "", "password")
	fs.StringVar(&c.database, "d", "oceanbase", "database")
}

func (c connFlags) open(ctx context.Context) (*obtop.Client, error) {
	return obtop.Open(ctx, obtop.Config{
		Host:     c.host,
		Port:     c.port,
		User:     c.user,
		Password: c.password,
		Database: c.database,
	})
}

func runSnapshot(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var conn connFlags
	addConnFlags(fs, &conn)
	format := fs.String("format", "json", "output format: json")
	user := fs.String("user", "", "filter user substring")
	dbName := fs.String("db", "", "filter database substring")
	keyword := fs.String("keyword", "", "filter keyword")
	activeOnly := fs.Bool("active-only", true, "exclude Sleep sessions from filtered view")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *format != "json" {
		fmt.Fprintf(os.Stderr, "unsupported format %q\n", *format)
		return exitUsage
	}

	client, err := conn.open(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return exitQueryError
	}
	defer client.Close()

	snap, err := client.Snapshot(ctx, obtop.SnapshotOptions{
		Filters: obtop.Filters{
			User:    *user,
			DBName:  *dbName,
			Keyword: *keyword,
		},
		ActiveOnly: *activeOnly,
		SortField:  obtop.SortByTime,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot: %v\n", err)
		return exitQueryError
	}

	rec := encodeSnapshotRecord(1, snap)
	if err := writeJSON(os.Stdout, rec); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		return exitQueryError
	}
	if snap.SessionsErr != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", snap.SessionsErr)
		return exitPartialFail
	}
	return exitOK
}

func runWatch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var conn connFlags
	addConnFlags(fs, &conn)
	format := fs.String("format", "ndjson", "output format: ndjson")
	intervalStr := fs.String("interval", "1s", "sample interval")
	durationStr := fs.String("duration", "0", "bounded duration (0 = until cancel)")
	user := fs.String("user", "", "filter user substring")
	dbName := fs.String("db", "", "filter database substring")
	keyword := fs.String("keyword", "", "filter keyword")
	activeOnly := fs.Bool("active-only", true, "exclude Sleep sessions from filtered view")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *format != "ndjson" && *format != "json" {
		fmt.Fprintf(os.Stderr, "unsupported format %q\n", *format)
		return exitUsage
	}
	interval, err := time.ParseDuration(*intervalStr)
	if err != nil || interval <= 0 {
		fmt.Fprintf(os.Stderr, "invalid interval %q\n", *intervalStr)
		return exitUsage
	}
	var duration time.Duration
	if *durationStr != "" && *durationStr != "0" {
		duration, err = time.ParseDuration(*durationStr)
		if err != nil || duration < 0 {
			fmt.Fprintf(os.Stderr, "invalid duration %q\n", *durationStr)
			return exitUsage
		}
	}

	client, err := conn.open(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return exitQueryError
	}
	defer client.Close()

	enc := json.NewEncoder(os.Stdout)
	seq := int64(0)
	wrote := false
	partial := false

	err = client.Watch(ctx, obtop.WatchOptions{
		Interval: interval,
		Duration: duration,
		Filters: obtop.Filters{
			User:    *user,
			DBName:  *dbName,
			Keyword: *keyword,
		},
		ActiveOnly: *activeOnly,
		SortField:  obtop.SortByTime,
	}, func(snap obtop.Snapshot) error {
		seq++
		rec := encodeSnapshotRecord(seq, snap)
		if err := enc.Encode(rec); err != nil {
			return err
		}
		wrote = true
		if snap.SessionsErr != nil {
			partial = true
			fmt.Fprintf(os.Stderr, "sessions seq=%d: %v\n", seq, snap.SessionsErr)
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return exitCancelled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			if partial {
				return exitPartialFail
			}
			return exitOK
		}
		fmt.Fprintf(os.Stderr, "watch: %v\n", err)
		if wrote {
			return exitPartialFail
		}
		return exitQueryError
	}
	if partial {
		return exitPartialFail
	}
	return exitOK
}

func runSQLHistory(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("sql-history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var conn connFlags
	addConnFlags(fs, &conn)
	format := fs.String("format", "ndjson", "output format: ndjson|json")
	sinceStr := fs.String("since", "5m", "lookback duration or RFC3339 timestamp")
	untilStr := fs.String("until", "", "end time RFC3339 (default now)")
	user := fs.String("user", "", "filter user substring")
	dbName := fs.String("db", "", "filter database substring")
	keyword := fs.String("keyword", "", "filter keyword in SQL")
	limit := fs.Int("limit", 200, "max records per page")
	cursorStr := fs.String("cursor", "", "continuation cursor as server_ip:port/request_id")
	fetchAll := fs.Bool("all", false, "auto-paginate until HasMore is false")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	since, err := parseSince(*sinceStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --since: %v\n", err)
		return exitUsage
	}
	var until time.Time
	if *untilStr != "" {
		until, err = time.Parse(time.RFC3339Nano, *untilStr)
		if err != nil {
			until, err = time.Parse(time.RFC3339, *untilStr)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --until: %v\n", err)
			return exitUsage
		}
	}
	var cursor *obtop.HistoryCursor
	if strings.TrimSpace(*cursorStr) != "" {
		cursor, err = parseHistoryCursor(*cursorStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --cursor: %v\n", err)
			return exitUsage
		}
	}
	if *fetchAll && *format == "json" {
		fmt.Fprintln(os.Stderr, "--all requires --format ndjson (JSON is single-page)")
		return exitUsage
	}

	client, err := conn.open(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return exitQueryError
	}
	defer client.Close()

	opts := obtop.HistoryOptions{
		Since:   since,
		Until:   until,
		User:    *user,
		DBName:  *dbName,
		Keyword: *keyword,
		Limit:   *limit,
		Cursor:  cursor,
	}

	page, err := client.QuerySQLHistory(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sql-history: %v\n", err)
		_ = writeHistoryOutput(os.Stdout, *format, page, 0)
		return exitQueryError
	}

	seqBase := int64(0)
	if err := writeHistoryOutput(os.Stdout, *format, page, seqBase); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		return exitQueryError
	}
	seqBase += int64(len(page.Records))

	// Freeze the first page's window so later pages do not chase time.Now().
	opts.Since = page.WindowStart
	opts.Until = page.WindowEnd

	worstCompleteness := page.Completeness
	worstReason := page.GapReason
	truncatedExport := page.HasMore
	if *fetchAll {
		for page.HasMore && page.NextCursor != nil {
			opts.Cursor = page.NextCursor
			page, err = client.QuerySQLHistory(ctx, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sql-history page: %v\n", err)
				_ = writeHistoryOutput(os.Stdout, *format, page, seqBase)
				return exitPartialFail
			}
			worstCompleteness, worstReason = mergeWorstCompleteness(
				worstCompleteness, worstReason, page.Completeness, page.GapReason,
			)
			if err := writeHistoryOutput(os.Stdout, *format, page, seqBase); err != nil {
				fmt.Fprintf(os.Stderr, "write: %v\n", err)
				return exitPartialFail
			}
			seqBase += int64(len(page.Records))
		}
		truncatedExport = page.HasMore
	}

	if worstCompleteness == obtop.CompletenessKnownGap || worstCompleteness == obtop.CompletenessUnknown {
		if worstReason != "" {
			fmt.Fprintf(os.Stderr, "completeness=%s: %s\n", worstCompleteness, worstReason)
		}
		return exitPartialFail
	}
	if truncatedExport {
		fmt.Fprintf(os.Stderr, "has_more=true: page truncated at limit %d; pass --cursor or --all to continue\n", *limit)
		if page.NextCursor != nil {
			fmt.Fprintf(os.Stderr, "next_cursor=%s\n", formatHistoryCursor(*page.NextCursor))
		}
		return exitPartialFail
	}
	return exitOK
}

func runExplain(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var conn connFlags
	addConnFlags(fs, &conn)
	format := fs.String("format", "json", "output format: json")
	sqlText := fs.String("sql", "", "SQL text to explain")
	dbName := fs.String("db-name", "", "database for USE before EXPLAIN")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*sqlText) == "" {
		fmt.Fprintln(os.Stderr, "--sql is required")
		return exitUsage
	}

	client, err := conn.open(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		return exitQueryError
	}
	defer client.Close()

	result, err := client.ExplainSQL(ctx, obtop.ExplainRequest{SQL: *sqlText, DBName: *dbName})
	if err != nil {
		fmt.Fprintf(os.Stderr, "explain: %v\n", err)
		return exitQueryError
	}
	rec := map[string]interface{}{
		"schema_version": obtop.SchemaVersion,
		"source":         "explain",
		"statement":      result.Statement,
		"db_name":        result.DBName,
		"columns":        result.Columns,
		"rows":           result.Rows,
		"notes":          result.Notes,
		"estimated":      result.Estimated,
	}
	if *format != "json" {
		fmt.Fprintf(os.Stderr, "unsupported format %q\n", *format)
		return exitUsage
	}
	if err := writeJSON(os.Stdout, rec); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		return exitQueryError
	}
	return exitOK
}

func writeHistoryOutput(w io.Writer, format string, page obtop.HistoryPage, seqBase int64) error {
	switch format {
	case "json":
		return writeJSON(w, encodeHistoryPage(page))
	case "ndjson":
		enc := json.NewEncoder(w)
		meta := encodeHistoryMeta(page)
		if err := enc.Encode(meta); err != nil {
			return err
		}
		for i, rec := range page.Records {
			if err := enc.Encode(encodeHistoryRecord(seqBase+int64(i+1), rec)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func encodeHistoryMeta(page obtop.HistoryPage) map[string]interface{} {
	meta := map[string]interface{}{
		"schema_version": obtop.SchemaVersion,
		"source":         "sql-history-meta",
		"completeness":   page.Completeness,
		"gap_reason":     page.GapReason,
		"has_more":       page.HasMore,
		"queried_at":     page.QueriedAt.UTC().Format(time.RFC3339Nano),
		"window_start":   page.WindowStart.UTC().Format(time.RFC3339Nano),
		"window_end":     page.WindowEnd.UTC().Format(time.RFC3339Nano),
		"next_cursors":   page.NextCursors,
		"record_count":   len(page.Records),
	}
	if page.NextCursor != nil {
		meta["next_cursor"] = formatHistoryCursor(*page.NextCursor)
	}
	return meta
}

func encodeHistoryPage(page obtop.HistoryPage) map[string]interface{} {
	records := make([]map[string]interface{}, 0, len(page.Records))
	for i, rec := range page.Records {
		records = append(records, encodeHistoryRecord(int64(i+1), rec))
	}
	out := map[string]interface{}{
		"schema_version": obtop.SchemaVersion,
		"source":         "sql-history",
		"completeness":   page.Completeness,
		"gap_reason":     page.GapReason,
		"has_more":       page.HasMore,
		"queried_at":     page.QueriedAt.UTC().Format(time.RFC3339Nano),
		"window_start":   page.WindowStart.UTC().Format(time.RFC3339Nano),
		"window_end":     page.WindowEnd.UTC().Format(time.RFC3339Nano),
		"next_cursors":   page.NextCursors,
		"records":        records,
	}
	if page.NextCursor != nil {
		out["next_cursor"] = formatHistoryCursor(*page.NextCursor)
	}
	return out
}

func encodeHistoryRecord(seq int64, rec obtop.HistoryRecord) map[string]interface{} {
	return map[string]interface{}{
		"schema_version": obtop.SchemaVersion,
		"seq":            seq,
		"source":         "sql-audit",
		"source_id":      rec.SourceID,
		"request_id":     rec.RequestID,
		"server_ip":      rec.ServerIP,
		"server_port":    rec.ServerPort,
		"tenant_id":      rec.TenantID,
		"user":           rec.User,
		"db_name":        rec.DBName,
		"client_ip":      rec.ClientIP,
		"sql":            rec.SQL,
		"request_time":   rec.RequestTime.UTC().Format(time.RFC3339Nano),
		"elapsed_us":     rec.ElapsedUS,
		"ret_code":       rec.RetCode,
		"trace_id":       rec.TraceID,
		"error":          nil,
	}
}

func encodeSnapshotRecord(seq int64, snap obtop.Snapshot) map[string]interface{} {
	rows := snap.Filtered
	if rows == nil {
		rows = snap.Rows
	}
	sessions := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, map[string]interface{}{
			"sess_id":       row.SessID,
			"proxy_sess_id": row.ProxySessID,
			"server_ip":     row.ServerIP,
			"user":          row.User,
			"host":          row.Host,
			"db_name":       row.DBName,
			"command":       row.Command,
			"time_sec":      row.Time,
			"total_time_sec": row.TotalTime,
			"state":         row.State,
			"sql":           row.SQL,
		})
	}

	errObj := map[string]interface{}{}
	if snap.SessionsErr != nil {
		errObj["sessions"] = obtop.AsDBError("snapshot", snap.SessionsErr)
	}
	if snap.MetricsErr != nil {
		errObj["metrics"] = obtop.AsDBError("metrics", snap.MetricsErr)
	}
	var errorField interface{}
	if len(errObj) > 0 {
		errorField = errObj
	}

	rates := map[string]interface{}{
		"ready":       snap.SQLRates.Ready,
		"warming_up":  snap.RatesWarmingUp,
		"stale":       snap.RatesStale,
		"values":      snap.SQLRates.Values,
		"last_success": nil,
	}
	if !snap.LastRatesSuccessAt.IsZero() {
		rates["last_success"] = snap.LastRatesSuccessAt.UTC().Format(time.RFC3339Nano)
	}

	return map[string]interface{}{
		"schema_version":  obtop.SchemaVersion,
		"seq":             seq,
		"source":          "processlist",
		"sampled_at":      snap.SampledAt.UTC().Format(time.RFC3339Nano),
		"actual_interval_ms": snap.ActualInterval.Milliseconds(),
		"active_only":     snap.ActiveOnly,
		"filters": map[string]string{
			"user":    snap.Filters.User,
			"db":      snap.Filters.DBName,
			"keyword": snap.Filters.Keyword,
		},
		"sessions":   sessions,
		"user_stats": snap.UserStats,
		"rates":      rates,
		"error":      errorField,
	}
}

func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func parseSince(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().Add(-5 * time.Minute), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-d), nil
}

func parseHistoryCursor(value string) (*obtop.HistoryCursor, error) {
	value = strings.TrimSpace(value)
	// Format: server_ip:port/request_id  (IPv4)
	slash := strings.LastIndex(value, "/")
	if slash < 0 {
		return nil, fmt.Errorf("want server_ip:port/request_id")
	}
	hostPort := value[:slash]
	reqID, err := strconv.ParseInt(value[slash+1:], 10, 64)
	if err != nil || reqID <= 0 {
		return nil, fmt.Errorf("invalid request_id")
	}
	colon := strings.LastIndex(hostPort, ":")
	if colon < 0 {
		return nil, fmt.Errorf("want server_ip:port/request_id")
	}
	port, err := strconv.ParseInt(hostPort[colon+1:], 10, 64)
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("invalid server_port")
	}
	ip := hostPort[:colon]
	if ip == "" {
		return nil, fmt.Errorf("empty server_ip")
	}
	return &obtop.HistoryCursor{ServerIP: ip, ServerPort: port, RequestID: reqID}, nil
}

func formatHistoryCursor(cursor obtop.HistoryCursor) string {
	return fmt.Sprintf("%s:%d/%d", cursor.ServerIP, cursor.ServerPort, cursor.RequestID)
}

// mergeWorstCompleteness keeps the most severe integrity status across pages.
// known_gap and unknown both outrank complete; known_gap is preferred when both appear.
func mergeWorstCompleteness(
	current obtop.Completeness, currentReason string,
	next obtop.Completeness, nextReason string,
) (obtop.Completeness, string) {
	rank := func(c obtop.Completeness) int {
		switch c {
		case obtop.CompletenessKnownGap:
			return 2
		case obtop.CompletenessUnknown:
			return 1
		default:
			return 0
		}
	}
	if rank(next) > rank(current) {
		if nextReason != "" {
			return next, nextReason
		}
		return next, currentReason
	}
	if rank(next) == rank(current) && currentReason == "" && nextReason != "" {
		return current, nextReason
	}
	return current, currentReason
}

func writeCLIUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  obtop [TUI options]                 # interactive TUI")
	fmt.Fprintln(w, "  obtop snapshot [options]            # single JSON snapshot")
	fmt.Fprintln(w, "  obtop watch [options]               # NDJSON continuous capture")
	fmt.Fprintln(w, "  obtop sql-history [options]         # GV$OB_SQL_AUDIT history")
	fmt.Fprintln(w, "  obtop explain --sql '...' [options] # explain saved SQL")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Shared connection flags: -h -P -u -p -d")
	fmt.Fprintln(w, "Exit codes: 0 ok, 1 query/connect error, 2 usage, 3 partial, 130 cancelled")
	fmt.Fprintln(w)
	config.WriteUsage(w)
}

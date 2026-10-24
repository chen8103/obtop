package obmodel

import "time"

// Completeness describes whether a history page's time window is known to be
// complete. Empty results must not be confused with unavailable or purged data.
type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessUnknown  Completeness = "unknown"
	CompletenessKnownGap Completeness = "known_gap"
)

// Snapshot is one sample of processlist sessions plus optional SQL rates.
// Time fields use wall-clock local time from the collector host.
// Session Time/TotalTime are seconds as reported by OceanBase.
type Snapshot struct {
	Rows               []Process
	Filtered           []Process
	UserStats          []UserSessionStats
	SQLRates           SQLRates
	SampledAt          time.Time
	ActualInterval     time.Duration
	Filters            Filters
	ActiveOnly         bool
	SortField          SortField
	SessionsErr        error
	MetricsErr         error
	RatesWarmingUp     bool
	RatesStale         bool
	LastRatesSuccessAt time.Time
}

// HistoryOptions controls GV$OB_SQL_AUDIT queries.
type HistoryOptions struct {
	Since      time.Time
	Until      time.Time
	User       string
	DBName     string
	Keyword    string
	Limit      int
	Cursor     *HistoryCursor
	ServerIP   string
	ServerPort int64
}

// HistoryCursor advances per observer node by REQUEST_ID.
type HistoryCursor struct {
	ServerIP   string `json:"server_ip"`
	ServerPort int64  `json:"server_port"`
	RequestID  int64  `json:"request_id"`
}

// HistoryRecord is one SQL audit entry with a stable source identity.
type HistoryRecord struct {
	SourceID     string    `json:"source_id"`
	RequestID    int64     `json:"request_id"`
	ServerIP     string    `json:"server_ip"`
	ServerPort   int64     `json:"server_port"`
	TenantID     int64     `json:"tenant_id,omitempty"`
	User         string    `json:"user"`
	DBName       string    `json:"db_name"`
	ClientIP     string    `json:"client_ip,omitempty"`
	SQL          string    `json:"sql"`
	RequestTime  time.Time `json:"request_time"`
	ElapsedUS    int64     `json:"elapsed_us"`
	IsHitPlan    bool      `json:"is_hit_plan,omitempty"`
	RetCode      int64     `json:"ret_code,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
}

// HistoryPage is one page of audit history with integrity metadata.
// Completeness describes the retention window (and enable-flag certainty).
// HasMore describes whether this export page hit the limit and more rows may remain.
type HistoryPage struct {
	Records      []HistoryRecord
	NextCursors  []HistoryCursor
	NextCursor   *HistoryCursor // convenience cursor for the last record (CLI continuation)
	HasMore      bool
	Completeness Completeness
	GapReason    string
	QueriedAt    time.Time
	WindowStart  time.Time
	WindowEnd    time.Time
}

// ExplainRequest asks for a freshly estimated plan of a saved SQL text.
type ExplainRequest struct {
	SQL    string
	DBName string
}

// ExplainResult is a structured EXPLAIN output (not the original execution plan).
type ExplainResult struct {
	Statement string
	DBName    string
	Columns   []string
	Rows      [][]string
	Notes     []string
	Estimated bool
	SessionID int64 // set when sourced via ExplainSession convenience path
}

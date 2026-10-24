package obtop

import (
	"time"

	"github.com/chen8103/obtop/obmodel"
)

// Config holds connection settings for Open.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// SnapshotOptions controls one Snapshot call.
type SnapshotOptions struct {
	Filters    obmodel.Filters
	ActiveOnly bool
	SortField  obmodel.SortField
}

// WatchOptions controls continuous sampling.
// Interval defaults to 1s. Duration 0 means until context cancel.
type WatchOptions struct {
	Interval   time.Duration
	Duration   time.Duration
	Filters    obmodel.Filters
	ActiveOnly bool
	SortField  obmodel.SortField
}

// Re-export commonly used public types so consumers need only import obtop.
type (
	Process           = obmodel.Process
	Filters           = obmodel.Filters
	SortField         = obmodel.SortField
	UserSessionStats  = obmodel.UserSessionStats
	SQLRates          = obmodel.SQLRates
	Snapshot          = obmodel.Snapshot
	HistoryOptions    = obmodel.HistoryOptions
	HistoryCursor     = obmodel.HistoryCursor
	HistoryRecord     = obmodel.HistoryRecord
	HistoryPage       = obmodel.HistoryPage
	ExplainRequest    = obmodel.ExplainRequest
	ExplainResult     = obmodel.ExplainResult
	Completeness      = obmodel.Completeness
	SessionDrainProgress = obmodel.SessionDrainProgress
	SessionDrainResult   = obmodel.SessionDrainResult
)

const (
	SortByTime    = obmodel.SortByTime
	SortByCommand = obmodel.SortByCommand
	SortByUser    = obmodel.SortByUser
	SortByDBName  = obmodel.SortByDBName

	CompletenessComplete = obmodel.CompletenessComplete
	CompletenessUnknown  = obmodel.CompletenessUnknown
	CompletenessKnownGap = obmodel.CompletenessKnownGap
)

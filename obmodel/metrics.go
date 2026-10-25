package obmodel

const (
	StatSQLSelect   int64 = 40000
	StatSQLInsert   int64 = 40002
	StatSQLReplace  int64 = 40004
	StatSQLUpdate   int64 = 40006
	StatSQLDelete   int64 = 40008
	StatSQLOther    int64 = 40018
	StatSQLCommit   int64 = 40025
	StatSQLRollback int64 = 40027
)

var SQLCounterStatIDs = []int64{
	StatSQLSelect,
	StatSQLInsert,
	StatSQLReplace,
	StatSQLUpdate,
	StatSQLDelete,
	StatSQLOther,
	StatSQLCommit,
	StatSQLRollback,
}

type SQLCounter struct {
	TenantID   int64
	ServerIP   string
	ServerPort int64
	StatID     int64
	Value      int64
}

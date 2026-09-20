package obtop

import (
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
)

// SchemaVersion is embedded in machine-readable protocol records.
const SchemaVersion = "1"

// DBError preserves OceanBase / MySQL driver error details for machine consumers.
type DBError struct {
	Op       string `json:"op"`
	Code     int    `json:"code,omitempty"`
	SQLState string `json:"sql_state,omitempty"`
	Message  string `json:"message"`
	Err      error  `json:"-"`
}

func (e *DBError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != 0 {
		return fmt.Sprintf("%s: Error %d (%s): %s", e.Op, e.Code, e.SQLState, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return e.Op
}

func (e *DBError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AsDBError extracts a DBError from err, wrapping plain errors when needed.
func AsDBError(op string, err error) *DBError {
	if err == nil {
		return nil
	}
	var dbErr *DBError
	if errors.As(err, &dbErr) {
		if dbErr.Op == "" {
			dbErr.Op = op
		}
		return dbErr
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return &DBError{
			Op:       op,
			Code:     int(mysqlErr.Number),
			SQLState: string(mysqlErr.SQLState[:]),
			Message:  mysqlErr.Message,
			Err:      err,
		}
	}
	return &DBError{Op: op, Message: err.Error(), Err: err}
}

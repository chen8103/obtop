package obclient

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

// ExplainStatement runs EXPLAIN on a saved SQL text. The plan is freshly
// estimated in the current environment and is not the original execution plan.
func (c *Client) ExplainStatement(ctx context.Context, req obmodel.ExplainRequest) (obmodel.ExplainResult, error) {
	statement := strings.TrimSpace(req.SQL)
	statement = strings.TrimSuffix(statement, ";")
	if statement == "" {
		return obmodel.ExplainResult{}, fmt.Errorf("empty sql text")
	}

	explainSQL, notes, err := prepareExplainSQL(statement)
	if err != nil {
		return obmodel.ExplainResult{}, err
	}
	notes = append(notes, "Plan is freshly estimated in the current environment, not the original execution plan.")

	explainCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := c.db.Conn(explainCtx)
	if err != nil {
		return obmodel.ExplainResult{}, fmt.Errorf("open dedicated explain connection: %w", err)
	}
	defer conn.Close()

	dbName := strings.TrimSpace(req.DBName)
	if dbName != "" {
		if _, err := conn.ExecContext(explainCtx, "USE "+quoteIdentifier(dbName)); err != nil {
			return obmodel.ExplainResult{}, fmt.Errorf("switch explain database to %q: %w", dbName, err)
		}
		notes = append(notes, "Using database "+dbName+" for EXPLAIN.")
	}

	rows, err := conn.QueryContext(explainCtx, "EXPLAIN EXTENDED_NOADDR "+explainSQL)
	if err != nil {
		return obmodel.ExplainResult{}, fmt.Errorf("explain sql: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return obmodel.ExplainResult{}, fmt.Errorf("read explain columns: %w", err)
	}

	values := make([]interface{}, len(columns))
	valueRefs := make([]interface{}, len(columns))
	for i := range columns {
		valueRefs[i] = &values[i]
	}

	data := make([][]string, 0, 8)
	for rows.Next() {
		if err := rows.Scan(valueRefs...); err != nil {
			return obmodel.ExplainResult{}, fmt.Errorf("scan explain row: %w", err)
		}
		record := make([]string, len(columns))
		for idx, value := range values {
			record[idx] = stringifyValue(value)
		}
		data = append(data, record)
	}
	if err := rows.Err(); err != nil {
		return obmodel.ExplainResult{}, fmt.Errorf("iterate explain rows: %w", err)
	}

	return obmodel.ExplainResult{
		Statement: statement,
		DBName:    dbName,
		Columns:   columns,
		Rows:      data,
		Notes:     notes,
		Estimated: true,
	}, nil
}

// ExplainSession looks up the current SQL for a session and explains it.
// Prefer ExplainStatement when the SQL text is already known.
func (c *Client) ExplainSession(ctx context.Context, sessID int64) (obmodel.ExplainResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var dbName sql.NullString
	var sqlText sql.NullString
	if err := c.db.QueryRowContext(queryCtx, sessionSQLQuery, sessID).Scan(&dbName, &sqlText); err != nil {
		if err == sql.ErrNoRows {
			return obmodel.ExplainResult{}, fmt.Errorf("session %d not found", sessID)
		}
		return obmodel.ExplainResult{}, fmt.Errorf("query session %d sql: %w", sessID, err)
	}

	result, err := c.ExplainStatement(ctx, obmodel.ExplainRequest{
		SQL:    sqlText.String,
		DBName: dbName.String,
	})
	if err != nil {
		return obmodel.ExplainResult{}, fmt.Errorf("explain session %d: %w", sessID, err)
	}
	result.SessionID = sessID
	result.Notes = append([]string{
		fmt.Sprintf("SQL was read from session %d at explain time.", sessID),
	}, result.Notes...)
	return result, nil
}

func stringifyValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func quoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// prepareExplainSQL validates statement type and replaces true placeholders
// with NULL. Placeholders inside string literals, quoted identifiers, and
// comments are left unchanged.
func prepareExplainSQL(statement string) (string, []string, error) {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" {
		return "", nil, fmt.Errorf("empty sql text")
	}

	firstToken := strings.ToUpper(firstWord(trimmed))
	switch firstToken {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE":
	default:
		return "", nil, fmt.Errorf("unsupported statement type %q", firstToken)
	}

	rewritten, replaced := replacePlaceholdersOutsideLiterals(trimmed)
	notes := make([]string, 0, 1)
	if replaced > 0 {
		notes = append(notes,
			fmt.Sprintf("Replaced %d placeholder '?' with NULL before running EXPLAIN; rewritten plan is not the original parameterized plan.", replaced),
		)
	}
	return rewritten, notes, nil
}

func replacePlaceholdersOutsideLiterals(sqlText string) (string, int) {
	var out strings.Builder
	out.Grow(len(sqlText))
	replaced := 0
	i := 0
	for i < len(sqlText) {
		ch := sqlText[i]

		// Line comments: -- or #
		if ch == '#' || (ch == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-') {
			end := i
			for end < len(sqlText) && sqlText[end] != '\n' {
				end++
			}
			out.WriteString(sqlText[i:end])
			i = end
			continue
		}

		// Block comments /* ... */
		if ch == '/' && i+1 < len(sqlText) && sqlText[i+1] == '*' {
			end := i + 2
			for end+1 < len(sqlText) && !(sqlText[end] == '*' && sqlText[end+1] == '/') {
				end++
			}
			if end+1 < len(sqlText) {
				end += 2
			}
			out.WriteString(sqlText[i:end])
			i = end
			continue
		}

		// Quoted string or identifier
		if ch == '\'' || ch == '"' || ch == '`' {
			end := i + 1
			quote := ch
			for end < len(sqlText) {
				if sqlText[end] == '\\' && end+1 < len(sqlText) {
					end += 2
					continue
				}
				if sqlText[end] == quote {
					// MySQL-style doubled quote escape
					if end+1 < len(sqlText) && sqlText[end+1] == quote {
						end += 2
						continue
					}
					end++
					break
				}
				end++
			}
			out.WriteString(sqlText[i:end])
			i = end
			continue
		}

		if ch == '?' {
			out.WriteString("NULL")
			replaced++
			i++
			continue
		}

		out.WriteByte(ch)
		i++
	}
	return out.String(), replaced
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

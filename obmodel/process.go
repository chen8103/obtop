package obmodel

import (
	"sort"
	"strconv"
	"strings"
)

type Process struct {
	SessID      int64
	ProxySessID int64
	ServerIP    string
	User        string
	Host        string
	DBName      string
	Command     string
	Time        int64
	TotalTime   int64
	State       string
	SQL         string
}

type SortField string

const (
	SortByTime    SortField = "TIME"
	SortByCommand SortField = "COMMAND"
	SortByUser    SortField = "USER"
	SortByDBName  SortField = "DB_NAME"
)

var SortCycle = []SortField{
	SortByTime,
	SortByCommand,
	SortByUser,
	SortByDBName,
}

type Filters struct {
	User    string
	DBName  string
	Keyword string
}

type UserSessionStats struct {
	User     string
	Active   int
	Inactive int
}

func ActiveProcesses(rows []Process) []Process {
	out := make([]Process, 0, len(rows))
	for _, row := range rows {
		if !isInactiveSession(row) {
			out = append(out, row)
		}
	}
	return out
}

func SessionStatsByUser(rows []Process) []UserSessionStats {
	statsByUser := make(map[string]UserSessionStats)
	for _, row := range rows {
		user := strings.TrimSpace(row.User)
		if user == "" {
			user = "(unknown)"
		}

		stats := statsByUser[user]
		stats.User = user
		if isInactiveSession(row) {
			stats.Inactive++
		} else {
			stats.Active++
		}
		statsByUser[user] = stats
	}

	stats := make([]UserSessionStats, 0, len(statsByUser))
	for _, stat := range statsByUser {
		stats = append(stats, stat)
	}
	sort.Slice(stats, func(i, j int) bool {
		return strings.ToLower(stats[i].User) < strings.ToLower(stats[j].User)
	})
	return stats
}

func KillSessionIDsByUser(rows []Process, user string) []int64 {
	targetUser := strings.TrimSpace(user)
	if targetUser == "" {
		return nil
	}

	seen := make(map[int64]struct{})
	sessionIDs := make([]int64, 0)
	for _, row := range rows {
		if strings.TrimSpace(row.User) != targetUser {
			continue
		}
		sessionID := row.SessID
		if sessionID <= 0 {
			continue
		}
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		sessionIDs = append(sessionIDs, sessionID)
	}
	return sessionIDs
}

func (f Filters) Summary() string {
	parts := make([]string, 0, 3)
	if f.User != "" {
		parts = append(parts, "user="+f.User)
	}
	if f.DBName != "" {
		parts = append(parts, "db="+f.DBName)
	}
	if f.Keyword != "" {
		parts = append(parts, "keyword="+f.Keyword)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func ApplyFilters(rows []Process, filters Filters) []Process {
	if filters == (Filters{}) {
		out := make([]Process, len(rows))
		copy(out, rows)
		return out
	}

	user := strings.ToLower(strings.TrimSpace(filters.User))
	dbName := strings.ToLower(strings.TrimSpace(filters.DBName))
	keyword := strings.ToLower(strings.TrimSpace(filters.Keyword))

	out := make([]Process, 0, len(rows))
	for _, row := range rows {
		if user != "" && !strings.Contains(strings.ToLower(row.User), user) {
			continue
		}
		if dbName != "" && !strings.Contains(strings.ToLower(row.DBName), dbName) {
			continue
		}
		if keyword != "" {
			haystack := strings.ToLower(row.SQL + " " + row.State + " " + row.Command + " " + row.ServerIP)
			if !strings.Contains(haystack, keyword) {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

func SortProcesses(rows []Process, field SortField) {
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]

		switch field {
		case SortByCommand:
			if left.Command == right.Command {
				return left.Time > right.Time
			}
			return strings.ToLower(left.Command) < strings.ToLower(right.Command)
		case SortByUser:
			if left.User == right.User {
				return left.Time > right.Time
			}
			return strings.ToLower(left.User) < strings.ToLower(right.User)
		case SortByDBName:
			if left.DBName == right.DBName {
				return left.Time > right.Time
			}
			return strings.ToLower(left.DBName) < strings.ToLower(right.DBName)
		case SortByTime:
			fallthrough
		default:
			if left.Time == right.Time {
				return rowIdentity(left) < rowIdentity(right)
			}
			return left.Time > right.Time
		}
	})
}

func NextSortField(current SortField) SortField {
	for idx, field := range SortCycle {
		if field == current {
			return SortCycle[(idx+1)%len(SortCycle)]
		}
	}
	return SortByTime
}

func rowIdentity(row Process) string {
	return strings.ToLower(strings.Join([]string{
		strconv.FormatInt(row.SessID, 10),
		row.ServerIP,
		row.User,
		row.Host,
		row.DBName,
		row.Command,
		row.State,
		row.SQL,
	}, "|"))
}

func isInactiveSession(row Process) bool {
	return strings.EqualFold(strings.TrimSpace(row.Command), "Sleep")
}

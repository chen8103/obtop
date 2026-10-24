package ui

import (
	"fmt"
	"strings"

	"github.com/chen8103/obtop/obmodel"
)

func formatExplainResult(result obmodel.ExplainResult) string {
	var builder strings.Builder

	if result.SessionID > 0 {
		builder.WriteString(fmt.Sprintf("Session ID: %d\n", result.SessionID))
	}
	if result.DBName != "" {
		builder.WriteString(fmt.Sprintf("Database: %s\n", result.DBName))
	}
	builder.WriteString("SQL:\n")
	builder.WriteString(result.Statement)
	builder.WriteString("\n\n")
	if len(result.Notes) > 0 {
		builder.WriteString("Note:\n")
		builder.WriteString(strings.Join(result.Notes, " "))
		builder.WriteString("\n\n")
	}
	if result.Estimated {
		builder.WriteString("Estimated: yes (freshly generated plan)\n\n")
	}

	if len(result.Rows) == 0 {
		builder.WriteString("EXPLAIN returned no rows.\n")
		return builder.String()
	}

	widths := make([]int, len(result.Columns))
	for i, column := range result.Columns {
		widths[i] = len(column)
	}
	for _, row := range result.Rows {
		for i, value := range row {
			if i < len(widths) && len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	for idx, column := range result.Columns {
		builder.WriteString(padRight(column, widths[idx]))
		if idx < len(result.Columns)-1 {
			builder.WriteString("  ")
		}
	}
	builder.WriteString("\n")

	for idx := range result.Columns {
		builder.WriteString(strings.Repeat("-", widths[idx]))
		if idx < len(result.Columns)-1 {
			builder.WriteString("  ")
		}
	}
	builder.WriteString("\n")

	for _, row := range result.Rows {
		for idx, value := range row {
			width := 0
			if idx < len(widths) {
				width = widths[idx]
			}
			builder.WriteString(padRight(value, width))
			if idx < len(row)-1 {
				builder.WriteString("  ")
			}
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
}

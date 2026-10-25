package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/chen8103/obtop/obmodel"
)

func (a *App) renderTable() {
	selectedKey := a.selectedRowKey()

	a.displayedRows = obmodel.ApplyFilters(obmodel.ActiveProcesses(a.rows), a.filters)
	obmodel.SortProcesses(a.displayedRows, a.sortField)

	a.table.Clear()
	headers := []string{"SESSID", "SVR_IP", "USER", "HOST", "DB", "COMMAND", "TIME", "STATE", "SQL"}
	for col, header := range headers {
		a.table.SetCell(0, col, headerCell(header))
	}

	for rowIndex, row := range a.displayedRows {
		tableRow := rowIndex + 1
		color := rowDurationColor(row)
		a.table.SetCell(tableRow, 0, cell(fmt.Sprintf("%d", row.SessID)).SetAlign(tview.AlignRight).SetTextColor(color))
		a.table.SetCell(tableRow, 1, cell(row.ServerIP).SetTextColor(color))
		a.table.SetCell(tableRow, 2, cell(row.User).SetTextColor(color))
		a.table.SetCell(tableRow, 3, cell(row.Host).SetTextColor(color))
		a.table.SetCell(tableRow, 4, cell(row.DBName).SetTextColor(color))
		a.table.SetCell(tableRow, 5, cell(row.Command).SetTextColor(color))
		a.table.SetCell(tableRow, 6, cell(fmt.Sprintf("%d", row.Time)).SetAlign(tview.AlignRight).SetTextColor(color))
		a.table.SetCell(tableRow, 7, cell(row.State).SetTextColor(color))
		a.table.SetCell(tableRow, 8, cell(truncate(row.SQL, 96)).SetTextColor(color))
	}

	if len(a.displayedRows) == 0 {
		a.table.SetSelectable(false, false)
		return
	}

	a.table.SetSelectable(true, false)
	for idx, row := range a.displayedRows {
		if rowKey(row) == selectedKey {
			a.table.Select(idx+1, 0)
			return
		}
	}
	a.table.Select(1, 0)
}

func (a *App) renderSessionStats() {
	if a.statsTable == nil {
		return
	}

	a.statsTable.Clear()
	headers := []string{"USER", "ACTIVE", "INACTIVE", "TOTAL"}
	for col, header := range headers {
		align := tview.AlignLeft
		if col > 0 {
			align = tview.AlignRight
		}
		a.statsTable.SetCell(0, col, tview.NewTableCell(header).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false).
			SetAlign(align))
	}

	for rowIndex, stat := range obmodel.SessionStatsByUser(a.rows) {
		tableRow := rowIndex + 1
		a.statsTable.SetCell(tableRow, 0, tview.NewTableCell(stat.User))
		a.statsTable.SetCell(tableRow, 1, tview.NewTableCell(fmt.Sprintf("%d", stat.Active)).SetAlign(tview.AlignRight))
		a.statsTable.SetCell(tableRow, 2, tview.NewTableCell(fmt.Sprintf("%d", stat.Inactive)).SetAlign(tview.AlignRight))
		a.statsTable.SetCell(tableRow, 3, tview.NewTableCell(fmt.Sprintf("%d", stat.Active+stat.Inactive)).SetAlign(tview.AlignRight))
	}
}

func (a *App) selectedRowKey() string {
	row, _ := a.table.GetSelection()
	if row <= 0 || row > len(a.displayedRows) {
		return ""
	}
	return rowKey(a.displayedRows[row-1])
}

func (a *App) selectedProcess() (obmodel.Process, bool) {
	row, _ := a.table.GetSelection()
	if row <= 0 || row > len(a.displayedRows) {
		return obmodel.Process{}, false
	}
	return a.displayedRows[row-1], true
}

func headerCell(text string) *tview.TableCell {
	return tview.NewTableCell(text).
		SetTextColor(tcell.ColorYellow).
		SetSelectable(false).
		SetExpansion(1)
}

func cell(text string) *tview.TableCell {
	return tview.NewTableCell(text).
		SetExpansion(1).
		SetMaxWidth(96)
}

func truncate(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if len([]rune(trimmed)) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func rowKey(row obmodel.Process) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s|%d|%s", row.SessID, row.ServerIP, row.User, row.Host, row.DBName, row.Command, row.Time, row.SQL)
}

func rowDurationColor(row obmodel.Process) tcell.Color {
	switch {
	case row.Time >= 120:
		return tcell.ColorRed
	case row.Time >= 30:
		return tcell.ColorOrange
	case row.Time >= 10:
		return tcell.ColorYellow
	default:
		return tcell.ColorWhite
	}
}

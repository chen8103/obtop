package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/chen8103/obtop/internal/config"
	"github.com/chen8103/obtop/obmodel"
)

func TestLimitStringRunes(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		limit     int
		want      string
		truncated bool
	}{
		{
			name:      "shorter than limit",
			input:     "select 1",
			limit:     maxSQLViewChars,
			want:      "select 1",
			truncated: false,
		},
		{
			name:      "exactly at limit",
			input:     strings.Repeat("a", maxSQLViewChars),
			limit:     maxSQLViewChars,
			want:      strings.Repeat("a", maxSQLViewChars),
			truncated: false,
		},
		{
			name:      "truncates unicode safely",
			input:     strings.Repeat("你", maxSQLViewChars+1),
			limit:     maxSQLViewChars,
			want:      strings.Repeat("你", maxSQLViewChars),
			truncated: true,
		},
		{
			name:      "zero limit",
			input:     "select 1",
			limit:     0,
			want:      "",
			truncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := limitStringRunes(tt.input, tt.limit)
			if got != tt.want {
				t.Fatalf("limitStringRunes() got %q, want %q", got, tt.want)
			}
			if truncated != tt.truncated {
				t.Fatalf("limitStringRunes() truncated=%v, want %v", truncated, tt.truncated)
			}
		})
	}
}

func TestFormatSQLView(t *testing.T) {
	t.Run("empty sql", func(t *testing.T) {
		got := formatSQLView("   ")
		if !strings.Contains(got, "No SQL selected.") {
			t.Fatalf("expected empty message, got %q", got)
		}
	})

	t.Run("keeps bracket content literal", func(t *testing.T) {
		sql := "select [literal] from dual"
		got := formatSQLView(sql)
		if !strings.Contains(got, "[literal]") {
			t.Fatalf("expected SQL body to preserve bracket content, got %q", got)
		}
	})

	t.Run("adds truncation notice", func(t *testing.T) {
		sql := strings.Repeat("x", maxSQLViewChars+5)
		got := formatSQLView(sql)
		if !strings.Contains(got, "[truncated: showing first 20000 Unicode chars]") {
			t.Fatalf("expected truncation notice, got %q", got)
		}
		if utf8.RuneCountInString(strings.Split(got, "\n\n")[0]) != maxSQLViewChars {
			t.Fatalf("expected displayed SQL to be capped at %d characters", maxSQLViewChars)
		}
	})

	t.Run("formats clauses across lines", func(t *testing.T) {
		sql := "select id, name from users where status = 1 and deleted = 0"
		got := formatSQLView(sql)
		if !strings.Contains(got, "SELECT\n  id,\n  name\nFROM\n  users\nWHERE\n  status = 1\n  AND deleted = 0") {
			t.Fatalf("expected formatted SQL output, got %q", got)
		}
	})
}

func TestToggleSQLWrap(t *testing.T) {
	app := newSQLTestApp()
	app.sqlWrap = true
	app.sqlText = "select 1"
	app.sqlSessID = 42

	app.toggleSQLWrap()

	if app.sqlWrap {
		t.Fatalf("expected sqlWrap to be false after toggle")
	}
	if text := app.sqlFooter.GetText(false); !strings.Contains(text, "wrap:off") {
		t.Fatalf("expected footer to show wrap off, got %q", text)
	}
	if text := app.sqlFooter.GetText(false); !strings.Contains(text, "auto wrap disabled") {
		t.Fatalf("expected footer note to mention wrap disabled, got %q", text)
	}
}

func TestCopySQLView(t *testing.T) {
	t.Run("copies full sql text", func(t *testing.T) {
		app := newSQLTestApp()
		expectedSQL := "select * from t"
		app.sqlText = expectedSQL

		var copied string
		done := make(chan struct{})
		app.clipboardWriter = func(ctx context.Context, text string) error {
			copied = text
			close(done)
			return nil
		}

		app.copySQLView()
		<-done
		waitForText(t, app.sqlFooter, "copied current SQL snapshot (15 Unicode chars)")

		if copied != expectedSQL {
			t.Fatalf("expected copied text %q, got %q", expectedSQL, copied)
		}
		if text := app.sqlFooter.GetText(false); !strings.Contains(text, "copied current SQL snapshot (15 Unicode chars)") {
			t.Fatalf("expected copy success note, got %q", text)
		}
	})

	t.Run("reports clipboard failure", func(t *testing.T) {
		app := newSQLTestApp()
		app.sqlText = "select 1"

		done := make(chan struct{})
		app.clipboardWriter = func(ctx context.Context, text string) error {
			close(done)
			return fmt.Errorf("boom")
		}

		app.copySQLView()
		<-done
		waitForText(t, app.sqlFooter, "copy failed: boom")

		if text := app.sqlFooter.GetText(false); !strings.Contains(text, "copy failed: boom") {
			t.Fatalf("expected copy failure note, got %q", text)
		}
	})
}

func TestShowSQLViewTogglesMainPanel(t *testing.T) {
	app := newSQLTestApp()
	app.displayedRows = []obmodel.Process{
		{SessID: 42, SQL: "select id, name from users where status = 1"},
	}
	app.table.Select(1, 0)

	app.showSQLView()

	if !app.sqlVisible {
		t.Fatalf("expected SQL panel to be visible")
	}
	if app.sqlSessID != 42 {
		t.Fatalf("expected sql session id 42, got %d", app.sqlSessID)
	}
	if !strings.Contains(app.sqlView.GetText(false), "SELECT\n  id,") {
		t.Fatalf("expected formatted SQL in panel, got %q", app.sqlView.GetText(false))
	}

	app.showSQLView()

	if app.sqlVisible {
		t.Fatalf("expected SQL panel to be hidden after second toggle")
	}
}

func TestRefreshSQLViewResetsScrollOffset(t *testing.T) {
	app := newSQLTestApp()
	app.sqlText = "select id, name from users where status = 1"
	app.sqlSessID = 42

	app.sqlView.ScrollTo(4, 7)
	app.refreshSQLView()

	row, column := app.sqlView.GetScrollOffset()
	if row != 0 || column != 0 {
		t.Fatalf("expected SQL view scroll offset reset to 0,0; got %d,%d", row, column)
	}
}

func TestKillUserConfirmationRequiresExactUsername(t *testing.T) {
	if !killUserConfirmed("app_writer", "app_writer") {
		t.Fatalf("exact username must confirm the batch kill")
	}
	if killUserConfirmed("app_writer", "APP_WRITER") {
		t.Fatalf("case-insensitive username must not confirm the batch kill")
	}
	if killUserConfirmed("app_writer", "app") {
		t.Fatalf("partial username must not confirm the batch kill")
	}
}

func TestUpdateStatusDisplaysErrorBeforeNote(t *testing.T) {
	app := &App{
		status:     tview.NewTextView().SetDynamicColors(true),
		sortField:  obmodel.SortByTime,
		statusNote: "batch kill user app: 0/1275 killed",
	}

	app.updateStatus(fmt.Errorf("probe session 101 failed: unknown thread id"))

	got := app.status.GetText(false)
	errorIndex := strings.Index(got, "Error:")
	noteIndex := strings.Index(got, "Note:")
	if errorIndex < 0 || noteIndex < 0 || errorIndex > noteIndex {
		t.Fatalf("status must display the actionable error before the summary note: %q", got)
	}
	if visible := app.status.GetText(true); !strings.HasPrefix(visible, "Error:") {
		t.Fatalf("status must lead with the actionable error so narrow terminals show it: %q", visible)
	}
}

func TestSessionDrainFailureDetailSurvivesSuccessfulRefresh(t *testing.T) {
	app := &App{
		status:    tview.NewTextView().SetDynamicColors(true),
		sortField: obmodel.SortByTime,
		mode:      modeKillUserRun,
	}
	result := obmodel.SessionDrainResult{
		User:       "app",
		Initial:    100,
		Remaining:  3,
		Rounds:     5,
		StopReason: obmodel.SessionDrainRoundLimit,
		Degraded:   true,
		Failures: []obmodel.SessionDrainFailure{
			{Command: "KILL PROXYSESSION", SessionID: 101, Err: fmt.Errorf("access denied")},
		},
	}

	app.finishSessionDrain("app", result, nil)
	app.updateStatus(nil)

	got := app.status.GetText(true)
	if !strings.Contains(got, "access denied") {
		t.Fatalf("successful refresh removed the batch failure detail: %q", got)
	}
}

func TestFinishSessionDrainReportsVerifiedZero(t *testing.T) {
	app := &App{
		status:    tview.NewTextView().SetDynamicColors(true),
		sortField: obmodel.SortByTime,
		mode:      modeKillUserRun,
	}

	app.finishSessionDrain("app", obmodel.SessionDrainResult{
		User:       "app",
		Initial:    100,
		Remaining:  0,
		Rounds:     2,
		StopReason: obmodel.SessionDrainCompleted,
	}, nil)

	got := app.status.GetText(true)
	if !strings.Contains(got, "drain user app complete: 100 -> 0 sessions, 2 rounds") {
		t.Fatalf("completion status = %q", got)
	}
}

func TestSessionDrainProgressIncludesCurrentTargets(t *testing.T) {
	app := &App{
		status:    tview.NewTextView().SetDynamicColors(true),
		sortField: obmodel.SortByTime,
	}

	app.updateSessionDrainProgress("app", obmodel.SessionDrainProgress{
		Round:         2,
		MaxRounds:     5,
		Initial:       100,
		Current:       12,
		ProxyTargets:  10,
		DirectTargets: 2,
	})

	got := app.status.GetText(true)
	if !strings.Contains(got, "round 2/5") ||
		!strings.Contains(got, "current 12") ||
		!strings.Contains(got, "proxy 10") ||
		!strings.Contains(got, "direct 2") {
		t.Fatalf("progress status = %q", got)
	}
}

func TestSessionDrainCanBeCancelledWithEscape(t *testing.T) {
	cancelled := false
	app := &App{
		status:    tview.NewTextView().SetDynamicColors(true),
		sortField: obmodel.SortByTime,
		mode:      modeKillUserRun,
		cancelSessionDrain: func() {
			cancelled = true
		},
	}

	event := tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone)
	if returned := app.handleKeys(event); returned != nil {
		t.Fatalf("Esc must be consumed while draining")
	}
	if !cancelled {
		t.Fatal("Esc did not cancel the session drain")
	}
	if got := app.status.GetText(true); !strings.Contains(got, "cancelling session drain") {
		t.Fatalf("cancel status = %q", got)
	}
}

func TestStatsViewStartsBatchKillConfirmationForSelectedUser(t *testing.T) {
	application := tview.NewApplication()
	statsTable := tview.NewTable().SetSelectable(true, false)
	input := tview.NewInputField()
	footer := tview.NewTextView()
	bottomPages := tview.NewPages().
		AddPage(footerPage, footer, true, true).
		AddPage(inputPage, input, true, false)
	pages := tview.NewPages().
		AddPage(mainPage, tview.NewBox(), true, false).
		AddPage(statsPage, tview.NewBox(), true, true)
	app := &App{
		app:         application,
		pages:       pages,
		statsTable:  statsTable,
		input:       input,
		bottomPages: bottomPages,
		status:      tview.NewTextView().SetDynamicColors(true),
		table:       tview.NewTable(),
		rows: []obmodel.Process{
			{SessID: 101, User: "app"},
			{SessID: 102, User: "app"},
		},
	}
	app.renderSessionStats()
	statsTable.Select(1, 0)

	event := tcell.NewEventKey(tcell.KeyRune, 'K', tcell.ModNone)
	if returned := app.handleStatsViewKey(event); returned != nil {
		t.Fatalf("batch kill shortcut was not consumed")
	}
	if string(app.mode) != "kill-user-input" {
		t.Fatalf("mode = %q, want kill-user-input", app.mode)
	}
	if !app.refreshPaused {
		t.Fatalf("refresh must pause while the batch target snapshot is confirmed")
	}
	if label := input.GetLabel(); !strings.Contains(label, `"app"`) || !strings.Contains(label, "2 sessions") {
		t.Fatalf("confirmation label does not identify the exact batch: %q", label)
	}
}

func TestNewRendersSQLPanelWithASCIIBorder(t *testing.T) {
	topLeft, topEdge, leftEdge := renderSQLPanelBorder(t, false)
	if topLeft != '+' || topEdge != '-' || leftEdge != '|' {
		t.Fatalf("normal SQL panel border = %q %q %q, want '+', '-', '|'", topLeft, topEdge, leftEdge)
	}
}

func TestNewRendersFocusedSQLPanelWithASCIIBorder(t *testing.T) {
	topLeft, topEdge, leftEdge := renderSQLPanelBorder(t, true)
	if topLeft != '+' || topEdge != '=' || leftEdge != '|' {
		t.Fatalf("focused SQL panel border = %q %q %q, want '+', '=', '|'", topLeft, topEdge, leftEdge)
	}
}

func TestRenderSessionStatsDisplaysActiveAndInactiveCountsByUser(t *testing.T) {
	app := &App{statsTable: tview.NewTable()}
	app.rows = []obmodel.Process{
		{User: "app", Command: "Query"},
		{User: "app", Command: "Sleep"},
		{User: "report", Command: "Execute"},
	}

	app.renderSessionStats()

	assertTableCellText(t, app.statsTable, 0, 0, "USER")
	assertTableCellText(t, app.statsTable, 0, 1, "ACTIVE")
	assertTableCellText(t, app.statsTable, 0, 2, "INACTIVE")
	assertTableCellText(t, app.statsTable, 0, 3, "TOTAL")
	assertTableCellText(t, app.statsTable, 1, 0, "app")
	assertTableCellText(t, app.statsTable, 1, 1, "1")
	assertTableCellText(t, app.statsTable, 1, 2, "1")
	assertTableCellText(t, app.statsTable, 1, 3, "2")
	assertTableCellText(t, app.statsTable, 2, 0, "report")
	assertTableCellText(t, app.statsTable, 2, 1, "1")
	assertTableCellText(t, app.statsTable, 2, 2, "0")
	assertTableCellText(t, app.statsTable, 2, 3, "1")
}

func TestRenderSessionStatsKeepsColumnsCompactAndNumbersAligned(t *testing.T) {
	app := &App{statsTable: tview.NewTable()}
	app.rows = []obmodel.Process{
		{User: "app", Command: "Query"},
		{User: "app", Command: "Sleep"},
		{User: "oss_meta_user", Command: "Sleep"},
	}
	app.renderSessionStats()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("initialize simulation screen: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(120, 5)
	app.statsTable.SetRect(0, 0, 120, 5)
	app.statsTable.Draw(screen)
	screen.Show()

	header := simulationScreenLine(screen, 0)
	appRow := simulationScreenLine(screen, 1)
	activeHeaderEnd := strings.Index(header, "ACTIVE") + len("ACTIVE")
	activeValueEnd := strings.Index(appRow, "1") + len("1")
	if activeHeaderEnd != activeValueEnd {
		t.Fatalf("ACTIVE header and value are misaligned:\n%s\n%s", header, appRow)
	}
	if totalColumn := strings.Index(header, "TOTAL"); totalColumn < 0 || totalColumn > 40 {
		t.Fatalf("statistics columns are not compact: TOTAL starts at column %d\n%s", totalColumn, header)
	}
}

func TestFormatSQLMetricsDisplaysTotalsAndStatementRates(t *testing.T) {
	rates := obmodel.SQLRates{
		Ready: true,
		Values: map[int64]float64{
			obmodel.StatSQLSelect:   40100,
			obmodel.StatSQLInsert:   1200,
			obmodel.StatSQLReplace:  0,
			obmodel.StatSQLUpdate:   1600,
			obmodel.StatSQLDelete:   12,
			obmodel.StatSQLOther:    88,
			obmodel.StatSQLCommit:   3300,
			obmodel.StatSQLRollback: 0,
		},
	}

	view := tview.NewTextView().
		SetDynamicColors(true).
		SetText(formatSQLMetrics(rates, nil))
	got := view.GetText(true)
	want := "OVERVIEW  OPS 46.3k/s  |  SQL QPS 43.0k/s  |  COMMIT 3.3k/s  |  ROLLBACK 0.0/s\n" +
		"READ      SELECT 40.1k/s\n" +
		"WRITE     INSERT 1.2k/s  |  UPDATE 1.6k/s  |  DELETE 12.0/s  |  REPLACE 0.0/s  |  OTHER 88.0/s"
	if got != want {
		t.Fatalf("visible SQL metrics = %q, want %q", got, want)
	}
}

func TestFormatSQLMetricsShowsUnavailableForMissingCounters(t *testing.T) {
	rates := obmodel.SQLRates{
		Ready: true,
		Values: map[int64]float64{
			obmodel.StatSQLSelect: 10,
		},
	}

	got := tview.NewTextView().
		SetDynamicColors(true).
		SetText(formatSQLMetrics(rates, nil)).
		GetText(true)
	if !strings.Contains(got, "OPS N/A") || !strings.Contains(got, "INSERT N/A") {
		t.Fatalf("missing counters must not be reported as zero: %q", got)
	}
}

func TestFormatSQLMetricsShowsErrorBeforeFirstValidSample(t *testing.T) {
	got := formatSQLMetrics(obmodel.SQLRates{}, fmt.Errorf("metrics unavailable"))
	if !strings.Contains(got, "OPS unavailable") || !strings.Contains(got, "metrics unavailable") {
		t.Fatalf("initial metrics error is not visible: %q", got)
	}
}

func TestFormatSQLMetricsMarksLastValidRatesStale(t *testing.T) {
	rates := obmodel.SQLRates{
		Ready: true,
		Values: map[int64]float64{
			obmodel.StatSQLSelect:   10,
			obmodel.StatSQLInsert:   0,
			obmodel.StatSQLReplace:  0,
			obmodel.StatSQLUpdate:   0,
			obmodel.StatSQLDelete:   0,
			obmodel.StatSQLOther:    0,
			obmodel.StatSQLCommit:   1,
			obmodel.StatSQLRollback: 0,
		},
	}

	got := tview.NewTextView().
		SetDynamicColors(true).
		SetText(formatSQLMetrics(rates, fmt.Errorf("metrics unavailable"))).
		GetText(true)
	if !strings.Contains(got, "OPS 11.0/s") || !strings.Contains(got, "STALE") {
		t.Fatalf("last valid metrics are not retained and marked stale: %q", got)
	}
}

func TestNewCreatesMainMetricsPanel(t *testing.T) {
	app := New(config.Config{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "oceanbase",
		Interval: time.Second,
	}, nil, nil, nil)

	if app.metricsView == nil {
		t.Fatalf("main metrics panel was not created")
	}
	if text := app.metricsView.GetText(false); !strings.Contains(text, "OPS warming up") {
		t.Fatalf("unexpected initial metrics text: %q", text)
	}
}

func newSQLTestApp() *App {
	table := tview.NewTable().SetSelectable(true, false)
	sqlPanel := tview.NewFlex()
	mainLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true).
		AddItem(sqlPanel, 0, 0, false)

	return &App{
		mainLayout: mainLayout,
		sqlPanel:   sqlPanel,
		table:      table,
		sqlHdr:     tview.NewTextView().SetDynamicColors(true),
		sqlView:    tview.NewTextView(),
		sqlFooter:  tview.NewTextView().SetDynamicColors(true),
		headerBase: "obtop - test",
		interval:   5 * time.Second,
		sqlWrap:    true,
		clipboardWriter: func(ctx context.Context, text string) error {
			return nil
		},
	}
}

func waitForText(t *testing.T, view *tview.TextView, want string) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(view.GetText(false), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %q in %q", want, view.GetText(false))
}

func assertTableCellText(t *testing.T, table *tview.Table, row, column int, want string) {
	t.Helper()

	cell := table.GetCell(row, column)
	if cell == nil || cell.Text != want {
		got := "<nil>"
		if cell != nil {
			got = cell.Text
		}
		t.Fatalf("cell (%d, %d) = %q, want %q", row, column, got, want)
	}
}

func simulationScreenLine(screen tcell.SimulationScreen, row int) string {
	cells, width, _ := screen.GetContents()
	line := make([]rune, width)
	for column := 0; column < width; column++ {
		cell := cells[row*width+column]
		if len(cell.Runes) == 0 {
			line[column] = ' '
			continue
		}
		line[column] = cell.Runes[0]
	}
	return string(line)
}

func renderSQLPanelBorder(t *testing.T, focused bool) (rune, rune, rune) {
	t.Helper()

	previousBorders := tview.Borders
	t.Cleanup(func() {
		tview.Borders = previousBorders
	})

	app := New(config.Config{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "oceanbase",
		Interval: time.Second,
	}, nil, nil, nil)
	app.sqlView.SetRect(0, 0, 40, 6)
	if focused {
		app.sqlView.Focus(func(tview.Primitive) {})
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("initialize simulation screen: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(40, 6)
	app.sqlView.Draw(screen)
	screen.Show()

	cells, width, _ := screen.GetContents()
	return cells[0].Runes[0], cells[1].Runes[0], cells[width].Runes[0]
}

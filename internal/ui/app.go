package ui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/chen8103/obtop/internal/config"
	"github.com/chen8103/obtop/obmodel"
	"github.com/chen8103/obtop/internal/service"
)

const (
	mainPage   = "main"
	helpPage   = "help"
	filterPage = "filter"
	statsPage  = "stats"

	footerPage  = "footer"
	inputPage   = "input"
	explainPage = "explain"
)

const maxSQLViewChars = 20000
const sqlPanelHeight = 12

type actionMode string

const (
	modeNormal         actionMode = "normal"
	modeExplainInput   actionMode = "explain-input"
	modeExplainRunning actionMode = "explain-running"
	modeExplainView    actionMode = "explain-view"
	modeKillInput      actionMode = "kill-input"
	modeKillConfirm    actionMode = "kill-confirm"
	modeKillUserInput  actionMode = "kill-user-input"
	modeKillUserRun    actionMode = "kill-user-running"
	modeHelpView       actionMode = "help-view"
	modeStatsView      actionMode = "stats-view"
	modeIntervalInput  actionMode = "interval-input"
	modeFilterInput    actionMode = "filter-input"
)

type SessionActions interface {
	KillSession(context.Context, int64) error
	DrainUserSessions(
		context.Context,
		string,
		func(obmodel.SessionDrainProgress),
	) (obmodel.SessionDrainResult, error)
	ExplainSession(context.Context, int64) (obmodel.ExplainResult, error)
	ExplainStatement(context.Context, obmodel.ExplainRequest) (obmodel.ExplainResult, error)
}

type App struct {
	app         *tview.Application
	pages       *tview.Pages
	mainLayout  *tview.Flex
	sqlPanel    *tview.Flex
	table       *tview.Table
	header      *tview.TextView
	metricsView *tview.TextView
	status      *tview.TextView
	footer      *tview.TextView
	input       *tview.InputField

	bottomPages *tview.Pages
	explainHdr  *tview.TextView
	explainView *tview.TextView
	sqlHdr      *tview.TextView
	sqlView     *tview.TextView
	sqlFooter   *tview.TextView
	helpHdr     *tview.TextView
	helpView    *tview.TextView
	statsTable  *tview.Table
	statsFooter *tview.TextView

	monitor         *service.Monitor
	actions         SessionActions
	quit            func()
	clipboardWriter func(context.Context, string) error

	sortField obmodel.SortField
	filters   obmodel.Filters

	rows            []obmodel.Process
	displayedRows   []obmodel.Process
	lastRefreshed   time.Time
	lastErr         error
	statusNote      string
	statusNoteStyle string
	interval        time.Duration

	headerBase string

	mode               actionMode
	pendingKillID      int64
	pendingKillUser    string
	cancelSessionDrain context.CancelFunc
	refreshPaused      bool
	sqlSessID          int64
	sqlText            string
	sqlWrap            bool
	sqlViewNote        string
	sqlCopying         bool
	sqlVisible         bool
	sqlFocused         bool
}

func configureASCIIBorders() {
	tview.Borders.Horizontal = '-'
	tview.Borders.Vertical = '|'
	tview.Borders.TopLeft = '+'
	tview.Borders.TopRight = '+'
	tview.Borders.BottomLeft = '+'
	tview.Borders.BottomRight = '+'
	tview.Borders.LeftT = '+'
	tview.Borders.RightT = '+'
	tview.Borders.TopT = '+'
	tview.Borders.BottomT = '+'
	tview.Borders.Cross = '+'
	tview.Borders.HorizontalFocus = '='
	tview.Borders.VerticalFocus = '|'
	tview.Borders.TopLeftFocus = '+'
	tview.Borders.TopRightFocus = '+'
	tview.Borders.BottomLeftFocus = '+'
	tview.Borders.BottomRightFocus = '+'
}

func New(cfg config.Config, monitor *service.Monitor, actions SessionActions, quit func()) *App {
	configureASCIIBorders()

	application := tview.NewApplication()
	table := tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)

	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText(cfg.HeaderTitle() + "  [yellow](Press '?' for help)[-]")

	metricsView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false).
		SetText(formatSQLMetrics(obmodel.SQLRates{}, nil))

	status := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)

	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetText("Keys: [yellow]s[-]=sort [yellow]d[-]=interval [yellow]f[-]=filter [yellow]t[-]=stats [yellow]v[-]=sql-panel [yellow]e[-]=explain [yellow]k[-]=kill [yellow]r[-]=refresh [yellow]q[-]=quit [yellow]?[-]=help")

	input := tview.NewInputField().
		SetFieldWidth(24)

	bottomPages := tview.NewPages().
		AddPage(footerPage, footer, true, true).
		AddPage(inputPage, input, true, false)

	explainHeader := tview.NewTextView().
		SetDynamicColors(true).
		SetText(cfg.HeaderTitle() + "  [yellow](EXPLAIN view, q/Esc to return)[-]")

	explainView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	explainView.SetBorder(true)
	explainView.SetTitle(" Explain ")

	explainFooter := tview.NewTextView().
		SetDynamicColors(true).
		SetText("Keys: [yellow]q[-]/[yellow]Esc[-]=back")

	explainLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(explainHeader, 1, 0, false).
		AddItem(explainView, 0, 1, true).
		AddItem(explainFooter, 1, 0, false)

	sqlHeader := tview.NewTextView().
		SetDynamicColors(true).
		SetText("SQL Panel")

	sqlView := tview.NewTextView().
		SetDynamicColors(false).
		SetScrollable(true).
		SetWrap(true)
	sqlView.SetBorder(true)
	sqlView.SetBorderPadding(0, 0, 1, 0)
	sqlView.SetTitle(" SQL ")

	sqlFooter := tview.NewTextView().
		SetDynamicColors(true).
		SetText("Keys: [yellow]c[-]=copy [yellow]w[-]=wrap [yellow]Tab[-]=focus [yellow]v[-]=hide")

	sqlLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(sqlHeader, 1, 0, false).
		AddItem(sqlView, 0, 1, true).
		AddItem(sqlFooter, 1, 0, false)

	mainLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(metricsView, 3, 0, false).
		AddItem(table, 0, 1, true).
		AddItem(sqlLayout, 0, 0, false).
		AddItem(status, 2, 0, false).
		AddItem(bottomPages, 1, 0, false)

	helpHeader := tview.NewTextView().
		SetDynamicColors(true).
		SetText(cfg.HeaderTitle() + "  [yellow](HELP view, q/Esc to return)[-]")

	helpView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	helpView.SetBorder(true)
	helpView.SetTitle(" Help ")

	helpFooter := tview.NewTextView().
		SetDynamicColors(true).
		SetText("Keys: [yellow]q[-]/[yellow]Esc[-]=back")

	helpLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(helpHeader, 1, 0, false).
		AddItem(helpView, 0, 1, true).
		AddItem(helpFooter, 1, 0, false)

	statsHeader := tview.NewTextView().
		SetDynamicColors(true).
		SetText(cfg.HeaderTitle() + "  [yellow](SESSION STATS, q/Esc to return)[-]")

	statsTable := tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)

	statsFooter := tview.NewTextView().
		SetDynamicColors(true).
		SetText("Counts include all current sessions. Keys: [yellow]K[-]=drain selected user [yellow]q[-]/[yellow]Esc[-]=back")

	statsLayout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(statsHeader, 1, 0, false).
		AddItem(statsTable, 0, 1, true).
		AddItem(statsFooter, 1, 0, false)

	pages := tview.NewPages().
		AddPage(mainPage, mainLayout, true, true).
		AddPage(explainPage, explainLayout, true, false).
		AddPage(helpPage, helpLayout, true, false).
		AddPage(statsPage, statsLayout, true, false)

	app := &App{
		app:             application,
		pages:           pages,
		mainLayout:      mainLayout,
		sqlPanel:        sqlLayout,
		table:           table,
		header:          header,
		metricsView:     metricsView,
		status:          status,
		footer:          footer,
		input:           input,
		bottomPages:     bottomPages,
		explainHdr:      explainHeader,
		explainView:     explainView,
		sqlHdr:          sqlHeader,
		sqlView:         sqlView,
		sqlFooter:       sqlFooter,
		helpHdr:         helpHeader,
		helpView:        helpView,
		statsTable:      statsTable,
		statsFooter:     statsFooter,
		monitor:         monitor,
		actions:         actions,
		quit:            quit,
		clipboardWriter: copyToClipboard,
		sortField:       obmodel.SortByTime,
		interval:        cfg.Interval,
		headerBase:      fmt.Sprintf("obtop - OceanBase Activity Monitor (%s/%s", cfg.Address(), cfg.Database),
		mode:            modeNormal,
		sqlWrap:         true,
	}

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			app.submitInput()
		}
	})
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			app.cancelInlineInput()
			return nil
		}
		return event
	})
	table.SetSelectionChangedFunc(func(row, column int) {
		app.handleTableSelectionChanged()
	})

	app.updateHeader()
	app.updateStatus(nil)
	application.SetRoot(pages, true)
	application.SetFocus(table)
	application.SetInputCapture(app.handleKeys)
	return app
}

func (a *App) Run(ctx context.Context) error {
	a.monitor.Start(ctx)

	go func() {
		for snapshot := range a.monitor.Updates() {
			snap := snapshot
			a.app.QueueUpdateDraw(func() {
				if a.refreshPaused {
					return
				}
				if snap.Err == nil {
					a.rows = cloneRows(snap.Rows)
				}
				a.lastRefreshed = snap.RefreshedAt
				a.lastErr = snap.Err
				a.metricsView.SetText(formatSQLMetrics(snap.SQLRates, snap.MetricsErr))
				a.renderTable()
				a.renderSessionStats()
				a.updateStatus(snap.Err)
			})
		}
	}()

	go func() {
		<-ctx.Done()
		a.app.Stop()
	}()

	return a.app.Run()
}

func (a *App) handleKeys(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.stop()
		return nil
	}

	switch a.mode {
	case modeExplainInput, modeKillInput, modeKillUserInput, modeFilterInput, modeIntervalInput:
		return event
	case modeKillConfirm:
		return a.handleKillConfirmKey(event)
	case modeExplainView:
		return a.handleExplainViewKey(event)
	case modeHelpView:
		return a.handleHelpViewKey(event)
	case modeStatsView:
		return a.handleStatsViewKey(event)
	case modeKillUserRun:
		if event.Key() == tcell.KeyEsc && a.cancelSessionDrain != nil {
			a.cancelSessionDrain()
			a.setStatusNote("cancelling session drain...", "yellow")
			a.updateStatus(nil)
		}
		return nil
	case modeExplainRunning:
		return nil
	}

	switch event.Key() {
	case tcell.KeyTAB:
		if a.sqlVisible {
			a.toggleSQLFocus()
			return nil
		}
	}

	switch event.Rune() {
	case 'q', 'Q':
		a.stop()
		return nil
	case '?':
		a.showHelp()
		return nil
	case 'd', 'D':
		a.showIntervalDialog()
		return nil
	case 'r':
		a.setStatusNote("manual refresh requested", "green")
		a.updateStatus(a.lastErr)
		a.monitor.TriggerRefresh()
		return nil
	case 's', 'S':
		a.sortField = obmodel.NextSortField(a.sortField)
		a.setStatusNote("sort changed to "+string(a.sortField), "green")
		a.renderTable()
		a.updateStatus(a.lastErr)
		return nil
	case 'f', 'F':
		a.showFilterDialog()
		return nil
	case 't', 'T':
		a.showSessionStats()
		return nil
	case 'c', 'C':
		if a.sqlVisible {
			a.copySQLView()
			return nil
		}
	case 'w', 'W':
		if a.sqlVisible {
			a.toggleSQLWrap()
			return nil
		}
	case 'v', 'V':
		a.showSQLView()
		return nil
	case 'k', 'K':
		a.showKillDialog()
		return nil
	case 'e', 'E':
		a.showExplainDialog()
		return nil
	default:
		return event
	}

	return event
}

func (a *App) showExplainDialog() {
	a.pauseRefresh()
	a.mode = modeExplainInput
	a.input.SetLabel("Explain session ID: ")
	a.input.SetText("")
	a.bottomPages.SwitchToPage(inputPage)
	a.app.SetFocus(a.input)
	a.setStatusNote("refresh paused; enter session id for explain", "yellow")
	a.updateStatus(a.lastErr)
}

func (a *App) showKillDialog() {
	a.pauseRefresh()
	a.mode = modeKillInput
	a.input.SetLabel("Kill session ID: ")
	a.input.SetText("")
	a.bottomPages.SwitchToPage(inputPage)
	a.app.SetFocus(a.input)
	a.setStatusNote("refresh paused; enter session id to kill", "yellow")
	a.updateStatus(a.lastErr)
}

func (a *App) showKillConfirm(sessID int64) {
	a.mode = modeKillConfirm
	a.pendingKillID = sessID
	a.setStatusNote(fmt.Sprintf("Kill session %d? press y to confirm, n to cancel", sessID), "red::b")
	a.updateStatus(a.lastErr)
	a.app.SetFocus(a.table)
}

func (a *App) showExplainResult(sessID int64, content string) {
	a.mode = modeExplainView
	a.explainView.SetTitle(fmt.Sprintf(" Explain %d ", sessID))
	a.explainView.SetText(content + "\n\nPress q or Esc to return.")
	a.pages.SwitchToPage(explainPage)
	a.app.SetFocus(a.explainView)
}

func (a *App) showSQLView() {
	if a.sqlVisible {
		a.hideSQLPanel()
		return
	}

	a.sqlVisible = true
	a.sqlFocused = false
	a.sqlViewNote = ""
	a.syncSQLPanelToSelection(false)
	a.showSQLPanel()
	if a.app != nil {
		a.app.SetFocus(a.table)
	}
}

func (a *App) showHelp() {
	a.mode = modeHelpView
	a.helpView.SetText(
		"Keyboard shortcuts\n\n" +
			"  s        Cycle sort field\n" +
			"  d        Change refresh interval in seconds\n" +
			"  f        Inline filters as user,db,keyword\n" +
			"  t        Show per-user session statistics\n" +
			"  K        Drain selected user sessions in session statistics view\n" +
			"  v        Toggle SQL panel on the main screen\n" +
			"  c        Copy current SQL snapshot when SQL panel is visible\n" +
			"  w        Toggle wrap when SQL panel is visible\n" +
			"  Tab      Switch focus between table and SQL panel\n" +
			"  e        Inline input session id for EXPLAIN\n" +
			"  k        Inline input session id, then y/n kill\n" +
			"  r        Refresh immediately\n" +
			"  q        Quit from main screen; close help/explain when those views are open\n" +
			"  Ctrl+C   Quit program\n" +
			"  ?        Show this help page\n\n" +
			"Filter input examples:\n" +
			"  dba,,\n" +
			"  ,test_db,\n" +
			"  dba,test_db,select\n",
	)
	a.pages.SwitchToPage(helpPage)
	a.app.SetFocus(a.helpView)
}

func (a *App) showSessionStats() {
	a.mode = modeStatsView
	a.renderSessionStats()
	a.pages.SwitchToPage(statsPage)
	if a.statsTable.GetRowCount() > 1 {
		a.statsTable.Select(1, 0)
	}
	a.app.SetFocus(a.statsTable)
}

func (a *App) showKillUserDialog() {
	row, _ := a.statsTable.GetSelection()
	if row <= 0 {
		return
	}
	cell := a.statsTable.GetCell(row, 0)
	if cell == nil {
		return
	}

	user := strings.TrimSpace(cell.Text)
	sessionIDs := obmodel.KillSessionIDsByUser(a.rows, user)
	if len(sessionIDs) == 0 {
		if a.statsFooter != nil {
			a.statsFooter.SetText("[red]No killable sessions remain for selected user.[-]  Keys: [yellow]q[-]/[yellow]Esc[-]=back")
		}
		return
	}

	a.pauseRefresh()
	a.mode = modeKillUserInput
	a.pendingKillUser = user
	a.pages.SwitchToPage(mainPage)
	a.input.SetLabel(fmt.Sprintf("Type user %q to DRAIN %d sessions: ", user, len(sessionIDs)))
	a.input.SetText("")
	a.bottomPages.SwitchToPage(inputPage)
	a.app.SetFocus(a.input)
	a.setStatusNote(fmt.Sprintf("DANGER: drain %d sessions for user %s", len(sessionIDs), user), "red::b")
	a.updateStatus(a.lastErr)
}

func (a *App) showIntervalDialog() {
	a.mode = modeIntervalInput
	a.input.SetLabel("Refresh interval (s): ")
	a.input.SetText(strconv.Itoa(int(a.interval.Seconds())))
	a.bottomPages.SwitchToPage(inputPage)
	a.app.SetFocus(a.input)
	a.setStatusNote("enter refresh interval in seconds", "green")
	a.updateStatus(a.lastErr)
}

func (a *App) showOverlay(name string, primitive tview.Primitive) {
	a.hideOverlay(name)
	a.pages.AddPage(name, primitive, true, true)
	a.app.SetFocus(primitive)
}

func (a *App) hideOverlay(name string) {
	if a.pages.HasPage(name) {
		a.pages.RemovePage(name)
	}
	a.app.SetFocus(a.table)
}

func (a *App) stop() {
	if a.quit != nil {
		a.quit()
	}
	a.app.Stop()
}

func (a *App) updateStatus(err error) {
	refreshed := "never"
	if !a.lastRefreshed.IsZero() {
		refreshed = a.lastRefreshed.Format("15:04:05")
	}

	summary := fmt.Sprintf(
		"Rows: [yellow]%d[-]  Sort: [yellow]%s[-]  Filters: [yellow]%s[-]  Last refresh: [yellow]%s[-]",
		len(a.displayedRows),
		a.sortField,
		a.filters.Summary(),
		refreshed,
	)
	text := summary
	if err != nil {
		text = "Error: [red]" + err.Error() + "[-]  " + summary
	}
	if a.statusNote != "" {
		style := a.statusNoteStyle
		if style == "" {
			style = "green"
		}
		text += fmt.Sprintf("  Note: [%s]%s[-]", style, a.statusNote)
	}
	if a.refreshPaused {
		text += "  [yellow]PAUSED[-]"
	}
	a.status.SetText(text)
}

func (a *App) updateHeader() {
	mainText := fmt.Sprintf("%s, every %s)  [yellow](Press '?' for help)[-]", a.headerBase, a.interval)
	a.header.SetText(mainText)
	a.explainHdr.SetText(fmt.Sprintf("%s, every %s)  [yellow](EXPLAIN view, q/Esc to return)[-]", a.headerBase, a.interval))
	a.updateSQLViewChrome()
	a.helpHdr.SetText(fmt.Sprintf("%s, every %s)  [yellow](HELP view, q/Esc to return)[-]", a.headerBase, a.interval))
}

func (a *App) setStatusNote(note, style string) {
	a.statusNote = note
	a.statusNoteStyle = style
}

func (a *App) clearStatusNote() {
	a.statusNote = ""
	a.statusNoteStyle = ""
}

func centered(width, height int, primitive tview.Primitive) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(
			tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(primitive, height, 1, true).
				AddItem(nil, 0, 1, false),
			width, 1, true,
		).
		AddItem(nil, 0, 1, false)
}

func cloneRows(rows []obmodel.Process) []obmodel.Process {
	out := make([]obmodel.Process, len(rows))
	copy(out, rows)
	return out
}

func killUserConfirmed(expectedUser, input string) bool {
	return expectedUser != "" && input == expectedUser
}

func (a *App) submitInput() {
	value := strings.TrimSpace(a.input.GetText())

	switch a.mode {
	case modeExplainInput:
		sessionID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || sessionID <= 0 {
			a.setStatusNote("invalid session id for explain", "red")
			a.updateStatus(a.lastErr)
			return
		}

		a.mode = modeExplainRunning
		a.bottomPages.SwitchToPage(footerPage)
		a.app.SetFocus(a.table)
		a.setStatusNote(fmt.Sprintf("explaining session %d...", sessionID), "yellow")
		a.updateStatus(nil)

		go func(sessID int64) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			result, explainErr := a.actions.ExplainSession(ctx, sessID)
			a.app.QueueUpdateDraw(func() {
				if explainErr != nil {
					a.lastErr = explainErr
					a.setStatusNote(fmt.Sprintf("explain failed for session %d", sessID), "red::b")
					a.showExplainResult(sessID, "EXPLAIN failed:\n\n"+explainErr.Error()+"\n\nPress q or Esc to return.")
					a.updateStatus(explainErr)
					return
				}
				a.setStatusNote(fmt.Sprintf("explain finished for session %d", sessID), "green")
				a.lastErr = nil
				a.updateStatus(nil)
				a.showExplainResult(sessID, formatExplainResult(result))
			})
		}(sessionID)
	case modeKillInput:
		sessionID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || sessionID <= 0 {
			a.setStatusNote("invalid session id for kill", "red")
			a.updateStatus(a.lastErr)
			return
		}

		a.bottomPages.SwitchToPage(footerPage)
		a.showKillConfirm(sessionID)
	case modeKillUserInput:
		if !killUserConfirmed(a.pendingKillUser, value) {
			a.setStatusNote("username confirmation mismatch; session drain not started", "red::b")
			a.updateStatus(a.lastErr)
			return
		}

		user := a.pendingKillUser
		a.pendingKillUser = ""
		a.mode = modeKillUserRun
		a.bottomPages.SwitchToPage(footerPage)
		a.app.SetFocus(a.table)
		a.setStatusNote(fmt.Sprintf("starting session drain for user %s...", user), "yellow")
		a.updateStatus(nil)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		a.cancelSessionDrain = cancel
		go func() {
			defer cancel()

			result, drainErr := a.actions.DrainUserSessions(
				ctx,
				user,
				func(progress obmodel.SessionDrainProgress) {
					a.app.QueueUpdateDraw(func() {
						a.updateSessionDrainProgress(user, progress)
					})
				},
			)
			a.app.QueueUpdateDraw(func() {
				a.finishSessionDrain(user, result, drainErr)
			})
		}()
	case modeIntervalInput:
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 {
			a.setStatusNote("invalid refresh interval", "red")
			a.updateStatus(a.lastErr)
			return
		}

		a.interval = time.Duration(seconds) * time.Second
		a.monitor.SetInterval(a.interval)
		a.updateHeader()
		a.mode = modeNormal
		a.bottomPages.SwitchToPage(footerPage)
		a.app.SetFocus(a.table)
		a.setStatusNote(fmt.Sprintf("refresh interval set to %ds", seconds), "green")
		a.updateStatus(a.lastErr)
	case modeFilterInput:
		user, dbName, keyword := parseInlineFilter(value)
		a.filters = obmodel.Filters{
			User:    user,
			DBName:  dbName,
			Keyword: keyword,
		}
		a.mode = modeNormal
		a.bottomPages.SwitchToPage(footerPage)
		a.app.SetFocus(a.table)
		a.renderTable()
		a.setStatusNote("filters updated", "green")
		a.updateStatus(a.lastErr)
	}
}

func (a *App) updateSessionDrainProgress(user string, progress obmodel.SessionDrainProgress) {
	a.setStatusNote(
		fmt.Sprintf(
			"draining user %s: round %d/%d, current %d, proxy %d, direct %d...",
			user,
			progress.Round,
			progress.MaxRounds,
			progress.Current,
			progress.ProxyTargets,
			progress.DirectTargets,
		),
		"yellow",
	)
	a.updateStatus(nil)
}

func (a *App) finishSessionDrain(
	user string,
	result obmodel.SessionDrainResult,
	drainErr error,
) {
	a.mode = modeNormal
	a.cancelSessionDrain = nil
	a.resumeRefresh()
	if drainErr != nil {
		a.lastErr = drainErr
		a.setStatusNote(fmt.Sprintf("session drain failed for user %s: %v", user, drainErr), "red::b")
		a.updateStatus(drainErr)
		return
	}

	a.lastErr = nil
	if len(result.Failures) > 0 {
		first := result.Failures[0]
		a.lastErr = fmt.Errorf(
			"%s session %d failed: %v",
			first.Command,
			first.SessionID,
			first.Err,
		)
	}

	if result.StopReason == obmodel.SessionDrainCompleted && result.Remaining == 0 {
		style := "green"
		note := fmt.Sprintf(
			"drain user %s complete: %d -> 0 sessions, %d rounds",
			user,
			result.Initial,
			result.Rounds,
		)
		if result.Degraded {
			style = "yellow"
			note += "; proxy kill unavailable, used connection fallback"
		}
		if result.RebuiltIDs > 0 {
			note += fmt.Sprintf("; %d backend IDs rebuilt", result.RebuiltIDs)
		}
		a.setStatusNote(
			note,
			style,
		)
		a.updateStatus(a.lastErr)
		if a.monitor != nil {
			a.monitor.TriggerRefresh()
		}
		return
	}

	note := fmt.Sprintf(
		"drain user %s incomplete: %d -> %d sessions after %d rounds (%s)",
		user,
		result.Initial,
		result.Remaining,
		result.Rounds,
		result.StopReason,
	)
	if result.Degraded {
		note += "; proxy kill unavailable"
	}
	if result.RebuiltIDs > 0 {
		note += fmt.Sprintf("; %d backend IDs rebuilt", result.RebuiltIDs)
	}
	if result.NewProxySessions > 0 {
		note += fmt.Sprintf("; %d new proxy sessions", result.NewProxySessions)
	}
	if len(result.Failures) > 0 {
		first := result.Failures[0]
		note += fmt.Sprintf(
			"; first %s session %d: %v",
			first.Command,
			first.SessionID,
			first.Err,
		)
	}
	a.setStatusNote(note, "red::b")
	a.updateStatus(a.lastErr)
	if a.monitor != nil {
		a.monitor.TriggerRefresh()
	}
}

func (a *App) cancelInlineInput() {
	if a.mode == modeExplainInput || a.mode == modeKillInput || a.mode == modeKillUserInput {
		a.resumeRefresh()
	}
	a.pendingKillUser = ""
	a.mode = modeNormal
	a.bottomPages.SwitchToPage(footerPage)
	a.app.SetFocus(a.table)
	a.clearStatusNote()
	a.updateStatus(a.lastErr)
}

func (a *App) handleKillConfirmKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEsc:
		a.mode = modeNormal
		a.pendingKillID = 0
		a.resumeRefresh()
		a.setStatusNote("kill cancelled", "yellow")
		a.updateStatus(a.lastErr)
		return nil
	}

	switch event.Rune() {
	case 'y', 'Y':
		sessionID := a.pendingKillID
		a.mode = modeNormal
		a.pendingKillID = 0
		a.setStatusNote(fmt.Sprintf("killing session %d...", sessionID), "yellow")
		a.updateStatus(nil)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := a.actions.KillSession(ctx, sessionID)
			a.app.QueueUpdateDraw(func() {
				if err != nil {
					a.resumeRefresh()
					a.clearStatusNote()
					a.lastErr = err
					a.updateStatus(err)
					return
				}
				a.setStatusNote(fmt.Sprintf("session %d killed", sessionID), "green")
				a.lastErr = nil
				a.resumeRefresh()
				a.updateStatus(nil)
				a.monitor.TriggerRefresh()
			})
		}()
		return nil
	case 'n', 'N', 'q', 'Q':
		a.mode = modeNormal
		a.pendingKillID = 0
		a.resumeRefresh()
		a.setStatusNote("kill cancelled", "yellow")
		a.updateStatus(a.lastErr)
		return nil
	default:
		return nil
	}
}

func (a *App) handleExplainViewKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEsc:
		a.closeExplainView()
		return nil
	}

	switch event.Rune() {
	case 'q', 'Q':
		a.closeExplainView()
		return nil
	default:
		return event
	}
}

func (a *App) handleHelpViewKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEsc:
		a.closeHelpView()
		return nil
	}

	switch event.Rune() {
	case 'q', 'Q', '?':
		a.closeHelpView()
		return nil
	default:
		return event
	}
}

func (a *App) handleStatsViewKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEsc:
		a.closeStatsView()
		return nil
	}

	switch event.Rune() {
	case 'K':
		a.showKillUserDialog()
		return nil
	case 'q', 'Q', 't', 'T':
		a.closeStatsView()
		return nil
	default:
		return event
	}
}

func (a *App) closeExplainView() {
	a.pages.SwitchToPage(mainPage)
	a.mode = modeNormal
	a.resumeRefresh()
	a.app.SetFocus(a.table)
}

func (a *App) showSQLPanel() {
	if a.mainLayout == nil || a.sqlPanel == nil {
		return
	}
	a.mainLayout.ResizeItem(a.sqlPanel, sqlPanelHeight, 0)
	a.updateSQLViewChrome()
}

func (a *App) hideSQLPanel() {
	a.sqlVisible = false
	a.sqlFocused = false
	a.sqlSessID = 0
	a.sqlText = ""
	a.sqlViewNote = ""
	a.sqlCopying = false
	if a.mainLayout != nil && a.sqlPanel != nil {
		a.mainLayout.ResizeItem(a.sqlPanel, 0, 0)
	}
	a.updateSQLViewChrome()
	if a.app != nil {
		a.app.SetFocus(a.table)
	}
}

func (a *App) closeHelpView() {
	a.pages.SwitchToPage(mainPage)
	a.mode = modeNormal
	a.app.SetFocus(a.table)
}

func (a *App) closeStatsView() {
	a.pages.SwitchToPage(mainPage)
	a.mode = modeNormal
	a.app.SetFocus(a.table)
}

func (a *App) pauseRefresh() {
	a.refreshPaused = true
}

func (a *App) resumeRefresh() {
	if !a.refreshPaused {
		return
	}
	a.refreshPaused = false
	if a.monitor != nil {
		a.monitor.TriggerRefresh()
	}
}

func parseInlineFilter(value string) (string, string, string) {
	parts := strings.SplitN(value, ",", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}

	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
}

func formatSQLView(sqlText string) string {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return "No SQL selected."
	}

	formatted := formatSQLForDisplay(trimmed)
	display, truncated := limitStringRunes(formatted, maxSQLViewChars)
	if !truncated {
		return display
	}

	return display + fmt.Sprintf("\n\n[truncated: showing first %d Unicode chars]", maxSQLViewChars)
}

func limitStringRunes(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", text != ""
	}

	runeCount := 0
	for idx := range text {
		if runeCount == limit {
			return text[:idx], true
		}
		runeCount++
	}

	return text, false
}

func formatSQLForDisplay(sqlText string) string {
	tokens := tokenizeSQL(sqlText)
	if len(tokens) == 0 {
		return sqlText
	}

	var builder strings.Builder
	clause := ""
	lineStart := true

	writeIndent := func(level int) {
		if !lineStart {
			return
		}
		builder.WriteString(strings.Repeat("  ", level))
		lineStart = false
	}
	writeToken := func(token string, indent int) {
		writeIndent(indent)
		builder.WriteString(token)
	}
	newLine := func() {
		if builder.Len() == 0 || strings.HasSuffix(builder.String(), "\n") {
			lineStart = true
			return
		}
		builder.WriteString("\n")
		lineStart = true
	}
	addSpaceIfNeeded := func(indent int) {
		if lineStart {
			writeIndent(indent)
			return
		}
		if !strings.HasSuffix(builder.String(), " ") && !strings.HasSuffix(builder.String(), "\n") && !strings.HasSuffix(builder.String(), "(") {
			builder.WriteString(" ")
		}
	}

	for i := 0; i < len(tokens); {
		if phrase, consumed, ok := sqlClauseAt(tokens, i); ok {
			switch phrase {
			case "AND", "OR":
				newLine()
				writeToken(phrase, 1)
				builder.WriteString(" ")
			case "ON":
				newLine()
				writeToken(phrase, 1)
				builder.WriteString(" ")
			default:
				if builder.Len() > 0 {
					newLine()
				}
				writeToken(phrase, 0)
				builder.WriteString("\n")
				lineStart = true
			}
			clause = phrase
			i += consumed
			continue
		}

		token := renderSQLToken(tokens[i])
		switch token {
		case ",":
			writeToken(",", 1)
			if clause == "SELECT" || clause == "SET" || clause == "VALUES" {
				builder.WriteString("\n")
				lineStart = true
			} else {
				builder.WriteString(" ")
			}
		case "(":
			if !lineStart && !strings.HasSuffix(builder.String(), " ") && !strings.HasSuffix(builder.String(), "\n") {
				builder.WriteString(" ")
			}
			writeToken("(", 1)
		case ")":
			writeToken(")", 1)
		default:
			indent := 0
			if clause != "" {
				indent = 1
			}
			addSpaceIfNeeded(indent)
			writeToken(token, indent)
		}
		i++
	}

	return strings.TrimSpace(builder.String())
}

func tokenizeSQL(sqlText string) []string {
	tokens := make([]string, 0, len(sqlText)/2)
	for i := 0; i < len(sqlText); {
		r, size := utf8.DecodeRuneInString(sqlText[i:])
		switch {
		case isSpaceRune(r):
			i += size
		case strings.HasPrefix(sqlText[i:], "--"):
			end := i + 2
			for end < len(sqlText) && sqlText[end] != '\n' {
				end++
			}
			tokens = append(tokens, sqlText[i:end])
			i = end
		case strings.HasPrefix(sqlText[i:], "/*"):
			end := i + 2
			for end < len(sqlText)-1 && sqlText[end:end+2] != "*/" {
				end++
			}
			if end < len(sqlText)-1 {
				end += 2
			} else {
				end = len(sqlText)
			}
			tokens = append(tokens, sqlText[i:end])
			i = end
		case r == '[':
			end := i + size
			for end < len(sqlText) {
				next, nextSize := utf8.DecodeRuneInString(sqlText[end:])
				end += nextSize
				if next == ']' {
					break
				}
			}
			tokens = append(tokens, sqlText[i:end])
			i = end
		case r == '\'' || r == '"' || r == '`':
			end := i + size
			for end < len(sqlText) {
				next, nextSize := utf8.DecodeRuneInString(sqlText[end:])
				end += nextSize
				if next == r {
					if end < len(sqlText) {
						escaped, escapedSize := utf8.DecodeRuneInString(sqlText[end:])
						if escaped == r {
							end += escapedSize
							continue
						}
					}
					break
				}
			}
			tokens = append(tokens, sqlText[i:end])
			i = end
		case isWordRune(r):
			end := i + size
			for end < len(sqlText) {
				next, nextSize := utf8.DecodeRuneInString(sqlText[end:])
				if !isWordRune(next) {
					break
				}
				end += nextSize
			}
			tokens = append(tokens, sqlText[i:end])
			i = end
		default:
			tokens = append(tokens, sqlText[i:i+size])
			i += size
		}
	}
	return tokens
}

func sqlClauseAt(tokens []string, idx int) (string, int, bool) {
	type clauseSpec struct {
		parts []string
		text  string
	}

	clauses := []clauseSpec{
		{parts: []string{"INSERT", "INTO"}, text: "INSERT INTO"},
		{parts: []string{"DELETE", "FROM"}, text: "DELETE FROM"},
		{parts: []string{"ORDER", "BY"}, text: "ORDER BY"},
		{parts: []string{"GROUP", "BY"}, text: "GROUP BY"},
		{parts: []string{"LEFT", "JOIN"}, text: "LEFT JOIN"},
		{parts: []string{"RIGHT", "JOIN"}, text: "RIGHT JOIN"},
		{parts: []string{"INNER", "JOIN"}, text: "INNER JOIN"},
		{parts: []string{"OUTER", "JOIN"}, text: "OUTER JOIN"},
		{parts: []string{"SELECT"}, text: "SELECT"},
		{parts: []string{"FROM"}, text: "FROM"},
		{parts: []string{"WHERE"}, text: "WHERE"},
		{parts: []string{"HAVING"}, text: "HAVING"},
		{parts: []string{"LIMIT"}, text: "LIMIT"},
		{parts: []string{"VALUES"}, text: "VALUES"},
		{parts: []string{"SET"}, text: "SET"},
		{parts: []string{"UPDATE"}, text: "UPDATE"},
		{parts: []string{"JOIN"}, text: "JOIN"},
		{parts: []string{"ON"}, text: "ON"},
		{parts: []string{"AND"}, text: "AND"},
		{parts: []string{"OR"}, text: "OR"},
	}

	for _, clause := range clauses {
		if idx+len(clause.parts) > len(tokens) {
			continue
		}
		matched := true
		for offset, part := range clause.parts {
			if strings.ToUpper(tokens[idx+offset]) != part {
				matched = false
				break
			}
		}
		if matched {
			return clause.text, len(clause.parts), true
		}
	}
	return "", 0, false
}

func renderSQLToken(token string) string {
	upper := strings.ToUpper(token)
	switch upper {
	case "SELECT", "FROM", "WHERE", "HAVING", "LIMIT", "VALUES", "SET", "UPDATE", "JOIN", "ON", "AND", "OR", "INTO", "DELETE", "INSERT", "LEFT", "RIGHT", "INNER", "OUTER", "GROUP", "ORDER", "BY", "AS", "DISTINCT":
		return upper
	default:
		return token
	}
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\n' || r == '\r' || r == '\t'
}

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || r == '.' || r == ':' ||
		(r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		r > 127
}

func (a *App) refreshSQLView() {
	a.sqlView.SetWrap(a.sqlWrap)
	if a.sqlSessID > 0 {
		a.sqlView.SetTitle(fmt.Sprintf(" SQL %d ", a.sqlSessID))
	} else {
		a.sqlView.SetTitle(" SQL ")
	}
	a.sqlView.SetText(formatSQLView(a.sqlText))
	a.sqlView.ScrollTo(0, 0)
	a.updateSQLViewChrome()
}

func (a *App) toggleSQLWrap() {
	a.sqlWrap = !a.sqlWrap
	if a.sqlWrap {
		a.sqlViewNote = "auto wrap enabled"
	} else {
		a.sqlViewNote = "auto wrap disabled"
	}
	a.refreshSQLView()
}

func (a *App) copySQLView() {
	if strings.TrimSpace(a.sqlText) == "" {
		a.sqlViewNote = "SQL is empty; nothing copied"
		a.updateSQLViewChrome()
		return
	}

	if a.sqlCopying {
		a.sqlViewNote = "copy already in progress"
		a.updateSQLViewChrome()
		return
	}

	a.sqlCopying = true
	a.sqlViewNote = "copying SQL snapshot..."
	a.updateSQLViewChrome()

	if a.app == nil {
		a.finishSQLCopy(a.runClipboardCopy(a.sqlText))
		return
	}

	go func(sqlText string) {
		a.finishSQLCopy(a.runClipboardCopy(sqlText))
	}(a.sqlText)
}

func (a *App) updateSQLViewChrome() {
	if a.sqlHdr == nil || a.sqlFooter == nil {
		return
	}

	wrapState := "off"
	if a.sqlWrap {
		wrapState = "on"
	}

	focusTarget := "sql"
	if a.sqlFocused {
		focusTarget = "table"
	}
	copyAction := "copy"
	if a.sqlCopying {
		copyAction = "copying"
	}

	headerText := "SQL Panel hidden"
	if a.sqlVisible {
		if a.sqlSessID > 0 {
			headerText = fmt.Sprintf("SQL Panel  Session:[yellow]%d[-]  Wrap:[yellow]%s[-]  Tab->[yellow]%s[-]", a.sqlSessID, wrapState, focusTarget)
		} else {
			headerText = fmt.Sprintf("SQL Panel  Wrap:[yellow]%s[-]  Tab->[yellow]%s[-]", wrapState, focusTarget)
		}
	}
	a.sqlHdr.SetText(headerText)

	footerText := fmt.Sprintf("Keys: [yellow]c[-]=%s [yellow]w[-]=wrap:%s [yellow]Tab[-]->%s [yellow]v[-]=hide", copyAction, wrapState, focusTarget)
	if a.sqlViewNote != "" {
		footerText += "  Note: [yellow]" + tview.Escape(a.sqlViewNote) + "[-]"
	}
	a.sqlHdr.SetDynamicColors(true)
	a.sqlFooter.SetDynamicColors(true)
	a.sqlFooter.SetText(footerText)
}

func (a *App) handleTableSelectionChanged() {
	if !a.sqlVisible {
		return
	}
	if a.sqlCopying {
		return
	}
	a.syncSQLPanelToSelection(false)
}

func (a *App) syncSQLPanelToSelection(preserveNote bool) {
	process, ok := a.selectedProcess()
	if !ok {
		if !a.sqlCopying && !preserveNote {
			a.sqlViewNote = ""
		}
		a.sqlSessID = 0
		a.sqlText = ""
		a.refreshSQLView()
		return
	}

	if !a.sqlCopying && !preserveNote {
		a.sqlViewNote = ""
	}
	a.sqlSessID = process.SessID
	a.sqlText = strings.TrimSpace(process.SQL)
	a.refreshSQLView()
}

func (a *App) toggleSQLFocus() {
	if !a.sqlVisible {
		return
	}

	if a.sqlFocused {
		a.sqlFocused = false
		if a.app != nil {
			a.app.SetFocus(a.table)
		}
	} else {
		a.sqlFocused = true
		if a.app != nil {
			a.app.SetFocus(a.sqlView)
		}
	}
	a.updateSQLViewChrome()
}

func (a *App) queueUIUpdate(fn func()) {
	if a.app != nil {
		a.app.QueueUpdateDraw(fn)
		return
	}
	fn()
}

func (a *App) runClipboardCopy(sqlText string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return sqlText, a.clipboardWriter(ctx, sqlText)
}

func (a *App) finishSQLCopy(sqlText string, err error) {
	count := utf8.RuneCountInString(sqlText)
	a.queueUIUpdate(func() {
		a.sqlCopying = false
		if err != nil {
			a.sqlViewNote = "copy failed: " + err.Error()
			a.updateSQLViewChrome()
			return
		}

		a.sqlViewNote = fmt.Sprintf("copied current SQL snapshot (%d Unicode chars)", count)
		if count > maxSQLViewChars {
			a.sqlViewNote += fmt.Sprintf("; view still shows first %d", maxSQLViewChars)
		}
		a.syncSQLPanelToSelection(true)
		a.updateSQLViewChrome()
	})
}

func copyToClipboard(ctx context.Context, text string) error {
	type clipboardCommand struct {
		name string
		args []string
	}

	var commands []clipboardCommand
	switch runtime.GOOS {
	case "darwin":
		commands = []clipboardCommand{{name: "pbcopy"}}
	case "windows":
		commands = []clipboardCommand{{name: "clip"}}
	default:
		commands = []clipboardCommand{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
	}

	var lastErr error
	for _, command := range commands {
		if _, err := exec.LookPath(command.name); err != nil {
			lastErr = err
			continue
		}

		cmd := exec.CommandContext(ctx, command.name, command.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	if lastErr != nil {
		return lastErr
	}

	return fmt.Errorf("no clipboard command available")
}

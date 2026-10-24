package ui

import "fmt"

func (a *App) showFilterDialog() {
	a.mode = modeFilterInput
	a.input.SetLabel("Filter user,db,keyword: ")
	a.input.SetText(fmt.Sprintf("%s,%s,%s", a.filters.User, a.filters.DBName, a.filters.Keyword))
	a.bottomPages.SwitchToPage(inputPage)
	a.app.SetFocus(a.input)
	a.setStatusNote("enter filters as user,db,keyword; leave blank to clear", "green")
	a.updateStatus(a.lastErr)
}

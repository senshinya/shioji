package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func branchPickerName(branch string) string {
	if branch == "" {
		return "分离 HEAD"
	}
	return branch
}

func (a *mobileApp) openBranchPicker(parent *mygo.Window, branches []string, selected string, choose func(string) bool) {
	var w *mygo.Window
	var list ui.ListState
	query, problem := "", ""
	resetList := func() { list = ui.ListState{} }
	w = a.page("选择分支", func(c *ui.Context) {
		p := palette(c)
		matches := branches
		if needle := strings.ToLower(strings.TrimSpace(query)); needle != "" {
			matches = make([]string, 0, len(branches))
			for _, branch := range branches {
				if strings.Contains(strings.ToLower(branchPickerName(branch)), needle) {
					matches = append(matches, branch)
				}
			}
		}
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			mobilePageContent(c, 0).Grow(1).Basis(0).MinHeight(0).Children(func() {
				search := c.Key("branch-search")
				mobileSearchField(search, ui.Row(search).Margin(0, 0, searchResultsGap), &query, "搜索分支", "搜索分支", func() {
					if query != "" {
						iconButton(search.Key("clear"), "清除搜索", closeSymbol, 13, func() {
							query = ""
							resetList()
						}).KeepFocus()
					}
				}).OnChange(resetList)
				if problem != "" {
					label(c.Key("branch-problem"), problem, captionTextSize, p.warning).Role(ui.RoleStatus)
				}
				if len(matches) == 0 {
					label(c.Key("branch-empty"), "没有匹配的分支", controlTextSize, p.secondary).Padding(10, 0).Role(ui.RoleStatus)
					return
				}
				list.Key = func(i int) any { return matches[i] }
				ui.List(c.Key("branches"), &list, len(matches), func(i int) {
					branch := matches[i]
					row := c.Key(branch)
					name := branchPickerName(branch)
					button := mobileButton(row, name, func() {
						if !a.pageCurrent(parent) || !a.pageCurrent(w) || !choose(branch) {
							problem = "分支列表已变化，请返回后重新选择。"
							return
						}
						w.Pop()
					}).KeepFocus().FillWidth().MinHeight(max(contentRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Padding(5, 0).Gap(8).
						AccessSelected(branch == selected)
					button.Children(func() {
						label(row, name, bodyTextSize, p.ink).Grow(1).MinWidth(0).MaxLines(2).Ellipsis("…").PassThrough()
						if branch == selected {
							alignedSymbol(row, checkSymbol, name, mobileStatusFont(c, bodyTextSize), inlineIconSize, p.ink)
						}
					})
				}).Grow(1).Basis(0).MinHeight(0)
			})
		})
	})
	a.pushNative(parent, w)
}

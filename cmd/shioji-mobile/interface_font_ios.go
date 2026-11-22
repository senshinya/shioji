package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func interfaceFontName(family string) string {
	if family == "system-ui" {
		return "系统字体"
	}
	return family
}

func (a *mobileApp) openInterfaceFont(parent *mygo.Window, commit func()) {
	families := []string{"system-ui"}
	for _, font := range ui.FontFamilies() {
		if !font.Registered && font.Name != "system-ui" {
			families = append(families, font.Name)
		}
	}
	var w *mygo.Window
	var list ui.ListState
	query := ""
	reset := func() { list = ui.ListState{} }
	w = a.page("界面字体", func(c *ui.Context) {
		p := palette(c)
		selected := a.model.Preferences.Normalized().InterfaceFont
		matches := families
		if needle := strings.ToLower(strings.TrimSpace(query)); needle != "" {
			matches = nil
			for _, family := range families {
				if strings.Contains(strings.ToLower(interfaceFontName(family)), needle) {
					matches = append(matches, family)
				}
			}
		}
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			mobilePageContent(c, 0).Grow(1).Basis(0).MinHeight(0).Children(func() {
				search := c.Key("font-search")
				mobileSearchField(search, ui.Row(search).Margin(0, 0, searchResultsGap), &query, "搜索字体", "搜索字体", func() {
					if query != "" {
						iconButton(search.Key("clear"), "清除搜索", closeSymbol, 13, func() {
							query = ""
							reset()
						}).KeepFocus()
					}
				}).OnChange(reset)
				if len(matches) == 0 {
					label(c, "没有匹配的字体", controlTextSize, p.secondary).Padding(10, 0).Role(ui.RoleStatus)
					return
				}
				list.Key = func(i int) any { return matches[i] }
				ui.List(c.Key("installed-fonts"), &list, len(matches), func(i int) {
					family := matches[i]
					row := c.Key(family)
					name := interfaceFontName(family)
					button := mobileButton(row, name, func() {
						if !a.pageCurrent(parent) || !a.pageCurrent(w) {
							return
						}
						if family != selected {
							a.model.Preferences.InterfaceFont = family
							commit()
						}
						w.Pop()
					}).FillWidth().MinHeight(max(contentRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Padding(5, 0).Gap(8).
						AccessSelected(family == selected)
					button.Children(func() {
						font := mobileStatusFont(row, bodyTextSize)
						font.Family = family + ", system-ui"
						oneLine(row, name, bodyTextSize, p.ink).Font(font.Family).Grow(1).PassThrough()
						if family == selected {
							alignedSymbol(row, checkSymbol, name, font, inlineIconSize, p.ink)
						}
					})
				}).Grow(1).Basis(0).MinHeight(0)
			})
		})
	})
	a.pushNative(parent, w)
}

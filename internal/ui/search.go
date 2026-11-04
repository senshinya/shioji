package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

func (v *View) searchControl(c *n.Context, t *n.Theme) {
	trigger := iconButton(c, searchIcon, "搜索")
	if trigger.Clicked() {
		v.searchOpen = !v.searchOpen
		v.searchSent = ""
	}
	n.PopoverBase(c.Key("search-panel"), trigger, &v.searchOpen, func(panel n.Element) {
		width, _ := c.Size()
		panel.Width(min(560, width-40)).Padding(10).Radius(10).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.TextInput(c.Key("search-input"), &v.searchText).FillWidth().Height(32).Label("搜索会话").Placeholder("搜索会话与对话内容…").FontSize(13).Changed()
			if v.Controller != nil && v.Model.Connected && v.searchSent != v.searchText {
				query := v.searchText
				accepted := v.do(app.Job{Method: "session.search", Input: protocol.SearchQuery{Query: query}, Apply: func(m *app.Model, b json.RawMessage, err error) {
					if query != v.searchText {
						return
					}
					if err != nil {
						m.Error = err.Error()
						return
					}
					json.Unmarshal(b, &v.searchResults)
				}})
				if accepted {
					v.searchSent = query
				} else {
					c.After(100 * time.Millisecond)
				}
			}
			results := v.searchResults
			if strings.TrimSpace(v.searchText) == "" {
				results = nil
				for i := len(v.Model.Snapshot.Sessions) - 1; i >= 0; i-- {
					s := v.Model.Snapshot.Sessions[i]
					results = append(results, domain.SearchResult{SessionID: s.ID, WorkspaceID: s.WorkspaceID, Title: s.Title})
				}
			}
			if len(results) == 0 {
				n.Text(c, "没有匹配的会话").FontSize(12).TextColor(t.TextMuted).Padding(10)
			}
			v.searchList.Key = func(i int) any { return results[i].SessionID }
			n.List(c, &v.searchList, len(results), func(i int) {
				r := results[i]
				workspace := ""
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.ID == r.WorkspaceID {
						workspace = w.Name
					}
				}
				row := n.ButtonBase(c).FillWidth().Padding(8).Radius(6).Label("搜索结果：" + r.Title + "，" + workspace).Children(func() {
					n.Column(c).FillWidth().Gap(4).Children(func() {
						n.Text(c, r.Title).SingleLine().FontSize(13)
						n.Text(c, workspace+" · 这台 Mac").FontSize(11).TextColor(t.TextMuted)
						if r.Snippet != "" {
							n.Text(c, r.Snippet).SingleLine().FontSize(12).TextColor(t.TextMuted)
						}
					})
				})
				if row.Clicked() {
					v.Model.WorkspaceID = r.WorkspaceID
					v.Model.SessionID = r.SessionID
					v.devices = false
					v.searchOpen = false
					v.branchOpen = false
				}
			}).FillWidth().Height(min(360, float32(len(results))*74))
		})
	})
}

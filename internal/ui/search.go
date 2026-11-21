package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"time"
)

func (v *View) resetSearchRequest() {
	v.searchRequest++
	v.searchSent, v.searchProblem = "", ""
	v.searchPending, v.searchFailed = 0, 0
	v.searchCursor = ""
}

func (v *View) toggleSearch(c *n.Context) {
	v.searchOpen = !v.searchOpen
	v.resetSearchRequest()
	if v.searchOpen {
		v.searchInput.Focus(c)
	} else {
		v.searchInput.CancelFocus(c)
	}
}

func (v *View) requestSearch(c *n.Context, query string) {
	v.resetSearchRequest()
	v.searchSent = query
	v.searchResultsQuery, v.searchResults = "", nil
	if v.Controller == nil {
		v.searchProblem = "没有可搜索的设备"
		return
	}
	request := v.searchRequest
	apply := func(update app.SearchProgress) {
		if !v.searchOpen || request != v.searchRequest || query != strings.TrimSpace(v.searchText) {
			return
		}
		v.searchResults, v.searchResultsQuery = update.Results, query
		v.searchPending, v.searchFailed = update.Pending, update.Failed
		if update.Error != nil {
			v.searchProblem = "搜索失败"
		}
	}
	v.searchPending = 1
	if !v.do(app.Job{
		Method: "session.search", Input: protocol.SearchQuery{Query: query},
		SearchProgress: func(_ *app.Model, update app.SearchProgress) { apply(update) },
		Apply: func(_ *app.Model, body json.RawMessage, err error) {
			var results []domain.SearchResult
			if err == nil {
				err = json.Unmarshal(body, &results)
			}
			apply(app.SearchProgress{Results: results, Error: err})
		},
	}) {
		v.searchSent = ""
		v.searchPending = 0
		v.searchProblem = "搜索繁忙"
		c.After(100 * time.Millisecond)
	}
}

func (v *View) searchControl(c *n.Context, t *n.Theme) {
	trigger := iconButton(c, searchIcon, "搜索")
	if trigger.Clicked() {
		v.toggleSearch(c)
	}
	panel := n.PopoverBase(c.Key("search-panel"), trigger, &v.searchOpen, func(panel n.Element) {
		width, _ := c.Size()
		entrance := popupMotion(c)
		entrance.Exit = nil
		panel.Width(min(560, width-40)).Padding(10).Radius(10).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Transition(entrance).Children(func() {
			field := textField(c.Key("search-input"), &v.searchText).FillWidth().Height(32).Label("搜索会话").Placeholder("搜索会话与对话内容…").FontSize(13).Bind(&v.searchInput)
			field.Changed()
			query := strings.TrimSpace(v.searchText)
			if query == "" {
				if v.searchSent != "" {
					v.resetSearchRequest()
				}
			} else if v.searchSent != query && (v.Controller != nil || v.searchResultsQuery != query) {
				v.requestSearch(c, query)
			}
			var results []domain.SearchResult
			if v.searchResultsQuery == query {
				results = v.searchResults
			}
			if query == "" {
				results = nil
				for i := len(v.Model.Snapshot.Sessions) - 1; i >= 0; i-- {
					s := v.Model.Snapshot.Sessions[i]
					archived := s.Archived
					for _, w := range v.Model.Snapshot.Workspaces {
						if w.ID == s.WorkspaceID {
							archived = archived || w.Archived
						}
					}
					results = append(results, domain.SearchResult{SessionID: s.ID, WorkspaceID: s.WorkspaceID, Title: s.Title, Archived: archived})
				}
			}
			field.OnTextKey(func(key n.TextKey) bool {
				if key.Mods != 0 || !v.searchOpen || len(results) == 0 || strings.TrimSpace(key.Text()) != query {
					return false
				}
				index := slices.IndexFunc(results, func(r domain.SearchResult) bool { return r.SessionID == v.searchCursor })
				switch key.Key {
				case n.KeyDown:
					index = min(index+1, len(results)-1)
				case n.KeyUp:
					if index < 0 {
						index = len(results)
					}
					index = max(0, index-1)
				case n.KeyEnter:
					v.openSearchResult(results[max(0, index)])
					return true
				default:
					return false
				}
				v.searchCursor = results[index].SessionID
				v.searchList.ScrollIntoView(index)
				return true
			})
			status := v.searchProblem
			if v.searchFailed > 0 && status == "" {
				status = "部分设备搜索失败，结果可能不完整"
			}
			if v.searchPending > 0 {
				if len(results) == 0 {
					status = "正在搜索…"
				} else {
					status = "正在搜索其他设备…"
				}
			}
			if len(results) == 0 && status == "" {
				status = "没有匹配的会话"
			}
			if status != "" {
				n.Row(c).FillWidth().Padding(4, 8).AlignItems(n.Center).Gap(8).Children(func() {
					n.Text(c, status).FontSize(12).TextColor(t.TextMuted).Grow(1).Role(n.RoleStatus)
					if v.searchPending == 0 && (v.searchProblem != "" || v.searchFailed > 0) && configurationAction(c, "重试搜索").Clicked() {
						v.requestSearch(c, query)
					}
				})
			}
			v.searchList.Key = func(i int) any { return results[i].SessionID }
			n.List(c, &v.searchList, len(results), func(i int) {
				r := results[i]
				workspace, device := "", ""
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.ID == r.WorkspaceID {
						workspace = w.Name
						device = v.Model.DeviceName(w.DeviceID)
					}
				}
				selected := field.Focused() && r.SessionID == v.searchCursor
				row := control(c).FillWidth().Padding(8).Radius(6).Label("搜索结果：" + r.Title + "，" + workspace).AccessSelected(selected)
				if selected {
					row.Background(t.Selection)
				}
				row.Children(func() {
					n.Column(c).FillWidth().Gap(4).Children(func() {
						n.Text(c, r.Title).SingleLine().FontSize(13)
						metadata := workspace + " · " + device
						if r.Archived {
							metadata += " · 已归档"
						}
						n.Text(c, metadata).FontSize(11).TextColor(t.TextMuted)
						if r.Snippet != "" {
							n.Text(c, r.Snippet).SingleLine().FontSize(12).TextColor(t.TextMuted)
						}
					})
				})
				if row.Clicked() {
					v.openSearchResult(r)
				}
			}).FillWidth().Height(min(360, float32(len(results))*74))
		})
	})
	if panel.Dismissed() {
		v.resetSearchRequest()
	}
}

func (v *View) openSearchResult(r domain.SearchResult) {
	v.workspaceEditor = nil
	v.navigateSession(r.SessionID, r.WorkspaceID)
	v.devices, v.searchOpen, v.branchOpen = false, false, false
	if r.ItemID != "" {
		v.splitRoot().prepareSecondary()
		pane := v.focusedView()
		s := pane.findState()
		s.mode, s.focus, s.placement = findPassive, false, findAwaitingHistory
		s.query, s.result, s.target = v.searchResultsQuery, r, findTarget{}
		pane.focusComposer = true
	}
}

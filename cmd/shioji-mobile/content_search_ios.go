package main

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type mobileContentSearch struct {
	query, submitted, problem string
	searchAt                  time.Time
	stop                      context.CancelFunc
	pending, failed           int
	results                   [][]mobileclient.DeviceSearchHit
	cursor                    [2]string
	list                      ui.ListState
	opening                   *mobileclient.DeviceSearchHit
	openDeadline              time.Time
	openError                 string
}

func searchHitKey(hit mobileclient.DeviceSearchHit) [2]string {
	return [2]string{hit.DeviceID, hit.SessionID}
}

func (s *mobileContentSearch) cancel() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
}

func (s *mobileContentSearch) change(query string) {
	term := strings.TrimSpace(query)
	if term == strings.TrimSpace(s.query) {
		s.query = query
		return
	}
	s.cancel()
	*s = mobileContentSearch{query: query}
	if term != "" {
		s.searchAt = time.Now().Add(220 * time.Millisecond)
	}
}

func (s *mobileContentSearch) read(a *mobileApp, w *mygo.Window) {
	defer w.Invalidate()
	s.cancel()
	*s = mobileContentSearch{query: s.query, submitted: strings.TrimSpace(s.query)}
	if s.submitted == "" {
		return
	}
	if len(s.submitted) > 256 || !utf8.ValidString(s.submitted) {
		s.problem = "查找内容最多 256 字节"
		return
	}
	if a.preview {
		var hits []mobileclient.DeviceSearchHit
		model := a.model
		for _, session := range model.Snapshot.Sessions {
			ws := a.workspace(session.WorkspaceID)
			hit := domain.SearchResult{SessionID: session.ID, WorkspaceID: ws.ID,
				Title: session.Title, Archived: session.Archived || ws.Archived}
			for _, item := range model.Details[session.ID].Items {
				if item.Kind == "queued" {
					continue
				}
				if start, _ := domain.FindLiteral(item.Text, s.submitted, 0); start >= 0 {
					hit.ItemID, hit.Snippet = item.ID, item.Text
					break
				}
			}
			if start, _ := domain.FindLiteral(session.Title, s.submitted, 0); hit.ItemID != "" || start >= 0 {
				hits = append(hits, mobileclient.DeviceSearchHit{SearchResult: hit, DeviceID: model.Snapshot.DeviceID,
					DeviceAddress: a.deviceName(), WorkspaceName: ws.Name, ThreadID: session.ThreadID})
			}
		}
		s.results = [][]mobileclient.DeviceSearchHit{hits}
		return
	}
	devices := a.savedDevices.Devices
	if len(devices) == 0 {
		s.problem = "没有可搜索的设备"
		return
	}
	signer, err := a.identity()
	if err != nil {
		s.problem = "无法读取设备连接身份"
		return
	}
	route, active, term := a.routeDial(), a.activeEndpoint(), s.submitted
	search, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.results, s.pending = make([][]mobileclient.DeviceSearchHit, len(devices)), len(devices)
	for index, device := range devices {
		go func() {
			ctx, cancel := context.WithTimeout(search, 15*time.Second)
			defer cancel()
			hits, err := mobileclient.SearchDevice(ctx, device, active, signer, route, term)
			mygo.RunOnMain(func() {
				if search.Err() != nil || !a.pageCurrent(w) {
					return
				}
				s.pending--
				if err != nil {
					s.failed++
				} else {
					s.results[index] = hits
				}
				w.Invalidate()
			})
		}()
	}
}

func (s *mobileContentSearch) open(a *mobileApp, w *mygo.Window, hit mobileclient.DeviceSearchHit) {
	if s.opening != nil || s.submitted == "" || s.submitted != strings.TrimSpace(s.query) {
		return
	}
	defer w.Invalidate()
	s.openError = ""
	if a.preview {
		a.finishContentSearch(w, hit.SearchResult)
		return
	}
	if err := a.openDeviceFromResults(w, hit.DeviceID); err != nil {
		s.openError = err.Error()
		return
	}
	s.opening, s.openDeadline = &hit, time.Now().Add(15*time.Second)
}

func (a *mobileApp) finishContentSearch(w *mygo.Window, hit domain.SearchResult) {
	if w == a.home {
		a.callUIKit("closeSearch", nil, nil)
	}
	a.openHistoryMatch(w, hit)
}

func (s *mobileContentSearch) update(c *ui.Context, a *mobileApp, w *mygo.Window) {
	if !s.searchAt.IsZero() {
		if delay := s.searchAt.Sub(c.Now()); delay > 0 {
			c.After(delay)
		} else {
			s.read(a, w)
		}
	}
	if s.opening == nil {
		return
	}
	hit := *s.opening
	switch {
	case a.model.Connected && a.model.Snapshot.DeviceID == hit.DeviceID:
		s.opening = nil
		source := a.sessionByID(hit.SessionID)
		if source.ID == "" || source.WorkspaceID != hit.WorkspaceID || source.ThreadID != hit.ThreadID {
			s.openError = "会话历史已改变，请重新搜索"
		} else {
			a.finishContentSearch(w, hit.SearchResult)
		}
	case !c.Now().Before(s.openDeadline):
		s.opening, s.openError = nil, "无法连接结果所属的设备"
	default:
		c.After(s.openDeadline.Sub(c.Now()))
	}
}

func (s *mobileContentSearch) hits() []mobileclient.DeviceSearchHit {
	var hits []mobileclient.DeviceSearchHit
	for _, group := range s.results {
		hits = append(hits, group...)
	}
	return hits
}

func (s *mobileContentSearch) move(direction int, hits []mobileclient.DeviceSearchHit) bool {
	if s.opening != nil || len(hits) == 0 {
		return false
	}
	index := slices.IndexFunc(hits, func(hit mobileclient.DeviceSearchHit) bool { return searchHitKey(hit) == s.cursor })
	if index < 0 && direction < 0 {
		index = len(hits)
	}
	index = max(0, min(index+direction, len(hits)-1))
	s.cursor = searchHitKey(hits[index])
	s.list.ScrollIntoView(index)
	return true
}

func (s *mobileContentSearch) submit(a *mobileApp, w *mygo.Window) {
	if s.opening != nil {
		return
	}
	hits := s.hits()
	if len(hits) == 0 {
		if s.pending == 0 {
			s.read(a, w)
		}
		return
	}
	index := slices.IndexFunc(hits, func(hit mobileclient.DeviceSearchHit) bool { return searchHitKey(hit) == s.cursor })
	s.open(a, w, hits[max(index, 0)])
}

func (s *mobileContentSearch) status(c *ui.Context, a *mobileApp, w *mygo.Window, count int) {
	if s.submitted == "" {
		return
	}
	status := ""
	switch {
	case s.opening != nil:
		status = "正在打开 " + a.deviceLabel(s.opening.DeviceID, s.opening.DeviceAddress) + " 的会话…"
	case s.openError != "":
		status = s.openError
	case s.problem != "":
		status = s.problem
	case s.pending > 0 && count == 0:
		status = "正在搜索…"
	case s.pending > 0:
		status = "正在搜索其他设备…"
	case s.failed > 0 && s.failed == len(s.results):
		status = "无法连接搜索设备"
	case s.failed > 0:
		status = "部分设备搜索失败，结果可能不完整"
	case count == 0:
		status = "没有匹配的会话"
	}
	if status != "" {
		ui.Row(c.Key("search-status")).AlignItems(ui.Center).PaddingX(8).Gap(8).Shrink(0).Children(func() {
			label(c, status, captionTextSize, palette(c).secondary).Role(ui.RoleStatus).Grow(1).MinWidth(0)
			if s.pending == 0 && s.failed > 0 && s.opening == nil {
				action(c, "重试搜索", func() { s.read(a, w) })
			}
		})
	}
}

func (s *mobileContentSearch) resultList(c *ui.Context, a *mobileApp, w *mygo.Window, hits []mobileclient.DeviceSearchHit, focused bool) {
	p := palette(c)
	s.list.Key = func(i int) any { return searchHitKey(hits[i]) }
	ui.List(c.Key("search-results"), &s.list, len(hits), func(i int) {
		hit := hits[i]
		row := c.Key(searchHitKey(hit))
		metadata := hit.WorkspaceName + " · " + a.deviceLabel(hit.DeviceID, hit.DeviceAddress)
		if hit.Archived {
			metadata += " · 已归档"
		}
		selected := focused && searchHitKey(hit) == s.cursor
		button := rowDivider(ui.ButtonBase(row).Column().AlignItems(ui.Start).FillWidth().Padding(8).Gap(4), p.line).
			Role(ui.RoleButton).Label(hit.Title).Value(metadata).Description(hit.Snippet).AccessSelected(selected).
			Disabled(s.opening != nil).OnClick(func() { s.open(a, w, hit) })
		if selected {
			button.Background(p.selected).Radius(cardRadius)
		}
		button.Children(func() {
			ui.Row(row.Key("heading")).FillWidth().AlignItems(ui.Baseline).Gap(8).PassThrough().Children(func() {
				oneLine(row, hit.Title, bodyTextSize, p.ink).FontWeight(620).Grow(1).MinWidth(0).PassThrough()
				oneLine(row, metadata, captionTextSize, p.secondary).MaxWidthPercent(55).MinWidth(0).Shrink(1).TextAlign(ui.End).PassThrough()
			})
			if hit.Snippet != "" {
				label(row, hit.Snippet, 13, p.secondary).MaxLines(2).Ellipsis("…").PassThrough()
			}
		})
	}).Grow(1).Basis(0).MinHeight(0)
}

func (a *mobileApp) openContentSearch(parent *mygo.Window, initial string) {
	if a.wide && parent == a.home {
		parent = a.detail
	}
	var w *mygo.Window
	var search mobileContentSearch
	query := initial
	w = a.page("全文搜索", func(c *ui.Context) {
		search.update(c, a, w)
		hits := search.hits()
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			mobilePageContent(c, 0).Grow(1).Basis(0).MinHeight(0).Children(func() {
				var field ui.Element
				ui.Column(c.Key("search-controls")).Padding(0, 0, searchResultsGap).Gap(4).Children(func() {
					const searchLabel = "搜索会话与对话内容"
					field = mobileSearchField(c, ui.Row(c), &query, searchLabel, searchLabel, func() {
						action(c, "搜索", func() { search.read(a, w) }).KeepFocus().Disabled(search.opening != nil)
					}).Disabled(search.opening != nil).ReturnKey(ui.ReturnSearch).OnChange(func() {
						search.change(query)
						w.Invalidate()
					}).OnSubmit(func() { search.submit(a, w) }).OnTextKey(func(key ui.TextKey) bool {
						if key.Mods != 0 || strings.TrimSpace(key.Text()) != search.submitted {
							return false
						}
						switch key.Key {
						case ui.KeyDown:
							return search.move(1, hits)
						case ui.KeyUp:
							return search.move(-1, hits)
						default:
							return false
						}
					})
					search.status(c, a, w, len(hits))
				})
				search.resultList(c, a, w, hits, field.Focused())
			})
		})
	})
	w.OnClosed(search.cancel)
	if initial != "" {
		search.change(initial)
		search.read(a, w)
	}
	a.pushNative(parent, w)
}

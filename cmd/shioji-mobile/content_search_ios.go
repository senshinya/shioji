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

func (a *mobileApp) openContentSearch(parent *mygo.Window, initial string) {
	if a.wide && parent == a.home {
		parent = a.detail
	}
	var w *mygo.Window
	var list ui.ListState
	var stop context.CancelFunc
	query, submitted, problem := initial, "", ""
	observed := strings.TrimSpace(initial)
	var searchAt time.Time
	pending, failed := 0, 0
	var results [][]mobileclient.DeviceSearchHit
	var cursor [2]string
	var opening *mobileclient.DeviceSearchHit
	var openDeadline time.Time
	openError := ""
	hitKey := func(hit mobileclient.DeviceSearchHit) [2]string { return [2]string{hit.DeviceID, hit.SessionID} }

	clear := func() {
		if stop != nil {
			stop()
			stop = nil
		}
		searchAt, submitted = time.Time{}, ""
		results, pending, failed, problem, openError = nil, 0, 0, "", ""
		cursor = [2]string{}
		list = ui.ListState{}
	}
	read := func() {
		defer w.Invalidate()
		clear()
		observed = strings.TrimSpace(query)
		submitted = observed
		if submitted == "" {
			return
		}
		if len(submitted) > 256 || !utf8.ValidString(submitted) {
			problem = "查找内容最多 256 字节"
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
					if start, _ := domain.FindLiteral(item.Text, submitted, 0); start >= 0 {
						hit.ItemID, hit.Snippet = item.ID, item.Text
						break
					}
				}
				if start, _ := domain.FindLiteral(session.Title, submitted, 0); hit.ItemID != "" || start >= 0 {
					hits = append(hits, mobileclient.DeviceSearchHit{SearchResult: hit, DeviceID: model.Snapshot.DeviceID,
						DeviceAddress: a.deviceName(), WorkspaceName: ws.Name, ThreadID: session.ThreadID})
				}
			}
			results = [][]mobileclient.DeviceSearchHit{hits}
			return
		}
		devices := a.savedDevices.Devices
		if len(devices) == 0 {
			problem = "没有可搜索的设备"
			return
		}
		signer, err := a.identity()
		if err != nil {
			problem = "无法读取设备连接身份"
			return
		}
		route, active, term := a.routeDial(), a.activeEndpoint(), submitted
		search, cancel := context.WithCancel(context.Background())
		stop = cancel
		results, pending = make([][]mobileclient.DeviceSearchHit, len(devices)), len(devices)
		for index, device := range devices {
			go func() {
				ctx, cancel := context.WithTimeout(search, 15*time.Second)
				defer cancel()
				hits, err := mobileclient.SearchDevice(ctx, device, active, signer, route, term)
				mygo.RunOnMain(func() {
					if search.Err() != nil || !a.pageCurrent(w) {
						return
					}
					pending--
					if err != nil {
						failed++
					} else {
						results[index] = hits
					}
					w.Invalidate()
				})
			}()
		}
	}

	open := func(hit mobileclient.DeviceSearchHit) {
		if opening != nil || submitted == "" || submitted != strings.TrimSpace(query) {
			return
		}
		defer w.Invalidate()
		if a.preview {
			a.openHistoryMatch(w, hit.SearchResult)
			return
		}
		openError = ""
		if err := a.openDeviceFromResults(w, hit.DeviceID); err != nil {
			openError = err.Error()
			return
		}
		opening, openDeadline = &hit, time.Now().Add(15*time.Second)
	}

	w = a.page("全文搜索", func(c *ui.Context) {
		if !searchAt.IsZero() {
			if delay := searchAt.Sub(c.Now()); delay > 0 {
				c.After(delay)
			} else {
				read()
			}
		}
		if opening != nil {
			hit := *opening
			switch {
			case a.model.Connected && a.model.Snapshot.DeviceID == hit.DeviceID:
				opening = nil
				source := a.sessionByID(hit.SessionID)
				if source.ID == "" || source.WorkspaceID != hit.WorkspaceID || source.ThreadID != hit.ThreadID {
					openError = "会话历史已改变，请重新搜索"
				} else {
					a.openHistoryMatch(w, hit.SearchResult)
				}
			case !c.Now().Before(openDeadline):
				opening, openError = nil, "无法连接结果所属的设备"
			default:
				c.After(openDeadline.Sub(c.Now()))
			}
		}
		var hits []mobileclient.DeviceSearchHit
		matching := submitted != "" && submitted == strings.TrimSpace(query)
		if matching {
			for _, group := range results {
				hits = append(hits, group...)
			}
		}
		cursorIndex := func() int {
			return slices.IndexFunc(hits, func(hit mobileclient.DeviceSearchHit) bool {
				return hitKey(hit) == cursor
			})
		}
		p := palette(c)
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			mobilePageContent(c, 0).Grow(1).Basis(0).MinHeight(0).Children(func() {
				var field ui.Element
				ui.Column(c.Key("search-controls")).Padding(0, 0, searchResultsGap).Gap(4).Children(func() {
					const searchLabel = "搜索会话与对话内容"
					field = mobileSearchField(c, ui.Row(c), &query, searchLabel, searchLabel, func() {
						action(c, "搜索", read).KeepFocus().Disabled(opening != nil)
					}).Disabled(opening != nil).ReturnKey(ui.ReturnSearch).OnChange(func() {
						term := strings.TrimSpace(query)
						if term == observed {
							return
						}
						observed = term
						clear()
						if term != "" {
							searchAt = time.Now().Add(220 * time.Millisecond)
						}
						w.Invalidate()
					}).OnSubmit(func() {
						if opening != nil {
							return
						}
						if len(hits) == 0 || strings.TrimSpace(query) != submitted {
							if pending == 0 || strings.TrimSpace(query) != submitted {
								read()
							}
							return
						}
						open(hits[max(cursorIndex(), 0)])
					}).OnTextKey(func(key ui.TextKey) bool {
						if key.Mods != 0 || opening != nil || len(hits) == 0 || strings.TrimSpace(key.Text()) != submitted {
							return false
						}
						index := cursorIndex()
						switch key.Key {
						case ui.KeyDown:
							index = min(index+1, len(hits)-1)
						case ui.KeyUp:
							if index < 0 {
								index = len(hits)
							}
							index = max(index-1, 0)
						default:
							return false
						}
						cursor = hitKey(hits[index])
						list.ScrollIntoView(index)
						w.Invalidate()
						return true
					})
					status := ""
					if matching {
						switch {
						case opening != nil:
							status = "正在打开 " + a.deviceLabel(opening.DeviceID, opening.DeviceAddress) + " 的会话…"
						case openError != "":
							status = openError
						case problem != "":
							status = problem
						case pending > 0 && len(hits) == 0:
							status = "正在搜索…"
						case pending > 0:
							status = "正在搜索其他设备…"
						case failed > 0 && failed == len(results):
							status = "无法连接搜索设备"
						case failed > 0:
							status = "部分设备搜索失败，结果可能不完整"
						case len(hits) == 0:
							status = "找不到匹配的消息"
						}
					}
					if status != "" {
						ui.Row(c.Key("search-status")).AlignItems(ui.Center).PaddingX(8).Gap(8).Children(func() {
							label(c, status, captionTextSize, p.secondary).Role(ui.RoleStatus).Grow(1).MinWidth(0)
							if pending == 0 && failed > 0 && opening == nil {
								action(c, "重试搜索", read)
							}
						})
					}
				})
				list.Key = func(i int) any { return hitKey(hits[i]) }
				fieldFocused := field.Focused()
				ui.List(c.Key("search-results"), &list, len(hits), func(i int) {
					hit := hits[i]
					row := c.Key(list.Key(i))
					metadata := hit.WorkspaceName + " · " + a.deviceLabel(hit.DeviceID, hit.DeviceAddress)
					if hit.Archived {
						metadata += " · 已归档"
					}
					selected := fieldFocused && hitKey(hit) == cursor
					button := rowDivider(ui.ButtonBase(row).Column().AlignItems(ui.Start).FillWidth().Padding(8).Gap(4), p.line).
						Role(ui.RoleButton).Label(hit.Title).Value(metadata).AccessSelected(selected).Disabled(opening != nil).OnClick(func() { open(hit) })
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
			})
		})
	})
	w.OnClosed(func() {
		if stop != nil {
			stop()
		}
	})
	if initial != "" {
		read()
	}
	a.pushNative(parent, w)
}

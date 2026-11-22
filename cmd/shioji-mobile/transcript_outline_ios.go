package main

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type transcriptJump struct {
	session, thread, item string
	center, ready         bool
	err                   string
	workSource            *domain.WorkActivity
}

func outlinePrompts(index []domain.HistoryPrompt, detail domain.SessionDetail) []domain.HistoryPrompt {
	prompts := slices.Clone(index)
	positions := make(map[string]int, len(prompts))
	for i, prompt := range prompts {
		positions[prompt.ID] = i
	}
	for _, item := range mobileclient.ConversationItems(detail) {
		if item.Kind != "user" || item.ID == "" {
			continue
		}
		prompt := domain.HistoryPrompt{ID: item.ID, Order: item.Order, Text: sentMessageText(mobileclient.SentMessageRuns(item))}
		if i, ok := positions[item.ID]; ok {
			if prompt.Order == 0 {
				prompt.Order = prompts[i].Order
			}
			prompts[i] = prompt
		} else {
			prompts = append(prompts, prompt)
		}
	}
	slices.SortStableFunc(prompts, func(a, b domain.HistoryPrompt) int {
		if a.Order == 0 || b.Order == 0 {
			switch {
			case a.Order > 0:
				return -1
			case b.Order > 0:
				return 1
			default:
				return 0
			}
		}
		return cmp.Compare(a.Order, b.Order)
	})
	return prompts
}

func outlinePromptTitle(text string) string {
	n := 0
	for i := range text {
		if n == 160 {
			text = text[:i] + "…"
			break
		}
		n++
	}
	if text = strings.Join(strings.Fields(text), " "); text != "" {
		return text
	}
	return "附件消息"
}

func (a *mobileApp) openConversationOutline(parent, conversation *mygo.Window, sid string) {
	source := a.sessionByID(sid)
	if source.ID == "" || !a.pageCurrent(conversation) {
		return
	}
	model := a.model
	var page *mygo.Window
	var index []domain.HistoryPrompt
	var before int64
	var request mobileRequest
	readErr := ""
	older, located := false, false
	var list ui.ListState
	current := func() bool {
		s := a.sessionByID(sid)
		return a.model == model && a.pageCurrent(conversation) && model.SessionID == sid &&
			s.WorkspaceID == source.WorkspaceID && s.ThreadID == source.ThreadID
	}
	load := func() {
		readErr = ""
		q := domain.HistoryQuery{SessionID: sid, ThreadID: source.ThreadID, Generation: model.Snapshot.Generation, Before: before, Limit: 100}
		a.pageJob(page, &request, "session.outline", q, decodePage(func(out domain.HistoryOutline) error {
			if !current() || out.SessionID != sid || out.ThreadID != source.ThreadID || model.Snapshot.Generation != q.Generation {
				return errors.New("会话历史已改变，请重新打开")
			}
			if out.Older {
				if len(out.Prompts) == 0 {
					return errors.New("无法继续读取更早的提问")
				}
				next := out.Prompts[len(out.Prompts)-1].Order
				if next <= 0 || q.Before > 0 && next >= q.Before {
					return errors.New("提问目录未继续推进")
				}
				before = next
			}
			index = append(index, out.Prompts...)
			older = out.Older
			return nil
		}))
	}
	jump := func(prompt domain.HistoryPrompt) {
		if !current() {
			return
		}
		finish := func(err error) {
			if err != nil {
				readErr = err.Error()
				page.Invalidate()
				return
			}
			a.historyJump = transcriptJump{session: sid, thread: source.ThreadID, item: prompt.ID}
			a.callUIKit("showPage", map[string]uintptr{"window": conversation.NativeHandle()}, nil)
			a.refresh()
		}
		detail := model.Details[sid]
		if slices.ContainsFunc(mobileclient.ConversationItems(detail), func(item domain.Item) bool { return item.ID == prompt.ID }) {
			finish(nil)
			return
		}
		readErr = ""
		detail.Session = source
		a.requestTranscriptHistory(sid, detail, domain.HistoryQuery{AroundID: prompt.ID}, page, finish)
	}
	page = a.page("对话导航", func(c *ui.Context) {
		p := palette(c)
		ui.Column(c).Fill().Padding(pageTopInset, 0, 0).Background(p.canvas).Children(func() {
			if !current() {
				mobileContent(c, contentMaxWidth).PaddingX(pageInset).Children(func() { mobileMessage(c, "会话已切换，请返回重新打开。") })
				return
			}
			detail := model.Details[sid]
			rows := outlinePrompts(index, detail)
			selected := ""
			if anchor := model.HistoryAnchors[sid]; anchor != "" {
				if i := slices.IndexFunc(detail.Items, func(item domain.Item) bool { return item.ID == anchor }); i >= 0 {
					order := detail.Items[i].Order
					for _, prompt := range rows {
						if prompt.ID == anchor || order > 0 && prompt.Order > 0 && prompt.Order <= order {
							selected = prompt.ID
						}
					}
				}
			}
			if selected == "" && len(rows) != 0 && (detail.History == nil || !detail.History.Newer) {
				selected = rows[len(rows)-1].ID
			}
			read := model.HistoryReads[sid]
			pending := read != nil && read.Pending
			mobileContent(c.Key("status"), contentMaxWidth).PaddingX(pageInset).Children(func() {
				switch {
				case request.busy || pending:
					mobileMessage(c, "正在读取历史…")
				case request.err != "":
					request.status(c)
					action(c, "重试读取提问", load).AlignSelf(ui.Start).Disabled(!model.Connected)
				case readErr != "":
					label(c, readErr, captionTextSize, p.warning).Padding(6, 0)
				case len(rows) == 0:
					mobileMessage(c, "没有已发送的提问")
				}
			})
			offset := 0
			if older {
				offset = 1
			}
			list.Key = func(i int) any {
				if i < offset {
					return transcriptOlder
				}
				return rows[i-offset].ID
			}
			if !located && len(rows) != 0 && !request.busy {
				if i := slices.IndexFunc(rows, func(p domain.HistoryPrompt) bool { return p.ID == selected }); i >= 0 {
					list.ScrollTo(i+offset, ui.Center)
				}
				located = true
			}
			ui.List(c.Key("outline"), &list, len(rows)+offset, func(i int) {
				row := c.Key(list.Key(i))
				mobileContent(row, contentMaxWidth).PaddingX(pageInset).Children(func() {
					if i < offset {
						action(row, "加载更早提问", load).AlignSelf(ui.Start).Disabled(request.busy || pending || !model.Connected)
						return
					}
					prompt := rows[i-offset]
					title := outlinePromptTitle(prompt.Text)
					button := mobileButton(row, title, func() { jump(prompt) }).FillWidth().Gap(10).AlignItems(ui.Center).
						Padding(8, 7).AccessSelected(prompt.ID == selected).Disabled(request.busy || pending)
					if prompt.ID == selected {
						button.Background(p.selected).Radius(9)
					} else {
						rowDivider(button, p.line)
					}
					button.Children(func() {
						label(row, title, controlTextSize, p.ink).Grow(1).MinWidth(0).MaxLines(2).Ellipsis("…").PassThrough()
						if prompt.ID == selected {
							ui.Icon(row, checkSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
						}
					})
				})
			}).Grow(1).Basis(0).MinHeight(0)
		})
	})
	if a.preview {
		if detail := model.Details[sid]; detail.History != nil && detail.History.Older {
			index = outlinePrompts(nil, previewOlderTranscript(detail))
		}
	} else if model.Connected {
		load()
	}
	a.pushNative(parent, page)
}

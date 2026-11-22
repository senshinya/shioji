package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobileRequest struct {
	busy bool
	err  string
}

func (r *mobileRequest) status(c *ui.Context) {
	if r.err != "" {
		label(c, r.err, 13, palette(c).warning).Padding(8, 0)
	} else if r.busy {
		mobileMessage(c, "正在处理…")
	}
}

// A page has its own result and error state. Jobs capture the current
// connection epoch and never update a page after it has left navigation.
func (a *mobileApp) pageJob(w *mygo.Window, state *mobileRequest, method string, input any, done func(json.RawMessage) error) {
	a.pageJobPrepared(w, state, method, input, nil, done)
}

func (a *mobileApp) pageJobPrepared(w *mygo.Window, state *mobileRequest, method string, input any, prepare func() error, done func(json.RawMessage) error) {
	a.runPageJob(nil, w, state, method, input, prepare, done)
}

func (a *mobileApp) runPageJob(ctx context.Context, w *mygo.Window, state *mobileRequest, method string, input any, prepare func() error, done func(json.RawMessage) error) {
	a.runPageJobResult(ctx, w, state, method, input, prepare, func(data json.RawMessage, err error) error {
		if err == nil && done != nil {
			return done(data)
		}
		return err
	})
}

func (a *mobileApp) runPageJobResult(ctx context.Context, w *mygo.Window, state *mobileRequest, method string, input any,
	prepare func() error, result func(json.RawMessage, error) error) {
	if state.busy || !a.pageCurrent(w) {
		return
	}
	selection := a.deviceSelection
	controller := a.controller
	state.err = ""
	if a.controller == nil || !a.model.Connected {
		state.err = "连接执行设备后再试"
		w.Invalidate()
		return
	}
	state.busy = true
	if !a.controller.Do(app.Job{Context: ctx, Method: method, Input: input, ConnectionEpoch: a.model.ConnectionEpoch, Prepare: prepare,
		Apply: func(_ *app.Model, data json.RawMessage, err error) {
			state.busy = false
			if !a.pageCurrent(w) || a.deviceSelection != selection || a.controller != controller || ctx != nil && ctx.Err() != nil {
				return
			}
			if err = result(data, err); err != nil {
				state.err = err.Error()
			}
			w.Invalidate()
		},
	}) {
		state.busy = false
		if err := result(nil, &protocol.RemoteError{Code: "notSent", Message: "请求繁忙，请稍后重试"}); err != nil {
			state.err = err.Error()
		}
	}
	w.Invalidate()
}

func decodePage[T any](done func(T) error) func(json.RawMessage) error {
	return func(data json.RawMessage) error {
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		return done(value)
	}
}

func (a *mobileApp) openPlan(parent *mygo.Window, sid string) {
	var w *mygo.Window
	w = a.page("计划与任务", func(c *ui.Context) {
		mobilePageBody(c, func() {
			d := a.model.Details[sid]
			mobileSection(c, "计划步骤")
			if d.Plan == nil {
				mobileMessage(c, "本轮暂无计划")
			} else {
				if d.Plan.Narrative != "" {
					markdown.View(c.Key("plan-narrative"), d.Plan.Narrative, nil).Selectable()
				}
				if d.Plan.Explanation != "" {
					mobileMessage(c, d.Plan.Explanation)
				}
				for i, step := range d.Plan.Steps {
					title := map[string]string{"completed": "已完成", "in_progress": "进行中", "pending": "待开始"}[step.Status]
					icon := infoSymbol
					if step.Status == "completed" {
						icon = checkSymbol
					} else if step.Status == "in_progress" {
						icon = clockSymbol
					}
					mobileRow(c.Key(i), step.Text, title, "", icon, nil)
				}
			}
			mobileGroupHeading(c, "本轮活动")
			a.workRows(c.Key("work-rows"), w, sid, "")
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) workRows(c *ui.Context, parent *mygo.Window, sid, kind string) {
	found := false
	for _, work := range domain.SessionWorks(a.model.Details[sid]) {
		if kind != "" && work.Kind != kind {
			continue
		}
		found = true
		icon := terminalSymbol
		kindName := "后台任务"
		if work.Kind == "agent" {
			icon = branchSymbol
			kindName = "子 Agent"
		}
		mobileRow(c.Key(work.ID), work.Title, kindName+" · "+workStateName(work.State), "", icon,
			func() { a.openWork(parent, sid, work) })
	}
	if !found {
		mobileMessage(c, "暂无")
	}
}

func workStateName(state string) string {
	if name := (map[string]string{"running": "运行中", "starting": "启动中", "waiting": "等待中", "completed": "已完成", "failed": "失败", "stopped": "已停止", "unknown": "待核对"})[state]; name != "" {
		return name
	}
	return state
}

func (a *mobileApp) openWork(parent *mygo.Window, sid string, work domain.WorkActivity) {
	req := mobileRequest{}
	read := mobileRequest{}
	var list ui.ListState
	var terminal codeViewport
	var terminalText string
	terminalFollowing := true
	var terminalLastScroll float32
	var tail replyTail
	list.FollowEnd = true
	key := a.model.WorkKey(sid, work.ID)
	source := a.sessionByID(sid)
	model := a.model
	ctx, cancel := context.WithCancel(context.Background())
	inputState := newMobileChildInput(model, key)
	var w *mygo.Window
	var attempted, paged bool
	load := func(before int64) {
		epoch, generation := model.ConnectionEpoch, model.Snapshot.Generation
		history := work.Kind == "agent" && model.WorkerMinor >= 41
		method := "session.work"
		var input any = protocol.WorkQuery{SessionID: sid, ID: work.ID}
		if history {
			method = "session.work.history"
			input = domain.WorkHistoryQuery{ID: work.ID, HistoryQuery: domain.HistoryQuery{
				SessionID: sid, ThreadID: source.ThreadID, Generation: generation,
				Before: before, Rounds: model.Preferences.HistoryPageRounds,
			}}
			if cache := model.WorkDetails[key]; !paged && cache.History == nil {
				// Preserve live observations made while the initial page is in flight.
				cache.History, cache.ThreadID = &domain.HistoryWindow{}, source.ThreadID
				model.WorkDetails[key] = cache
			}
		}
		attempted = true
		a.runPageJob(ctx, w, &read, method, input, nil, decodePage(func(detail domain.WorkDetail) error {
			if a.model != model || model.ConnectionEpoch != epoch || model.Snapshot.Generation != generation ||
				detail.SessionID != sid || detail.Work.ID != work.ID || a.sessionByID(sid).ThreadID != source.ThreadID ||
				history && (detail.ThreadID != source.ThreadID || detail.History == nil) {
				return fmt.Errorf("执行任务历史已改变，请重试")
			}
			if before > 0 {
				model.MergeWorkHistoryPage(detail)
			} else {
				model.MergeWorkDetail(detail)
				inputState.ready = true
			}
			paged = history
			return nil
		}))
	}
	draw := func(c *ui.Context) {
		if current := a.sessionByID(sid); !a.preview && (current.ID == "" || current.ThreadID != source.ThreadID) {
			mobilePageBody(c, func() { mobileMessage(c, "会话历史已改变，请返回重新打开此页面") })
			return
		}
		if !a.preview && !attempted && a.model.Connected {
			load(0)
		}
		d := a.model.WorkDetails[key]
		current := d.Work
		if current.ID == "" {
			current = work
		}
		header := func(c *ui.Context) {
			if current.State != "" {
				live := a.model.Connected && (current.State == "running" || current.State == "starting")
				mobileProgressRow(c, workStateName(current.State), current.StartedAt, live, func(sweep *mobileToolSweep) {
					switch {
					case current.DurationMS != nil:
						oneLine(c, mobileElapsed(*current.DurationMS), captionTextSize, palette(c).secondary)
					case current.StartedAt != nil && !current.StartedAt.IsZero() && (live || current.CompletedAt != nil && !current.CompletedAt.IsZero()):
						mobileClock(c, *current.StartedAt, current.CompletedAt, mobileStatusFont(c, captionTextSize), sweep)
					}
				})
			}
			if current.Brief != "" && current.Kind != "background" {
				label(c, current.Brief, bodyTextSize, palette(c).ink).Selectable().Padding(8, 0)
			}
			if d.Truncated {
				mobileMessage(c, "仅显示最近的输出")
			}
		}
		session := a.sessionByID(sid)
		items := mobileclient.WorkConversationItems(d, a.model.Details[sid].Requests)
		conversation := current.Kind == "agent" && len(items) != 0
		var order int64
		for _, item := range items {
			order = max(order, item.Order)
		}
		tail.observe(c.Now(), replyHistoryScope{session: work.ID, thread: source.ThreadID, connection: a.model.ConnectionEpoch},
			order, items, conversation && a.model.Connected && inputState.ready && list.FollowingEnd() && !c.Preferences().ReduceMotion, nil)
		var replies map[string]string
		if conversation {
			replies = childReplyFooters(items)
		}
		entry := func(row *ui.Context, item domain.Item) {
			switch item.Kind {
			case "tool", "activity", "reasoning":
				a.toolItem(row, w, session, item, nil, nil, false)
			case "request":
				a.requestSource(row, w, sid, item, a.model.Snapshot.PendingRequests, true, tail.receiptProgress(row, item.ID), func() {
					if row.Preferences().ReduceMotion {
						list.ScrollToEnd()
					} else {
						list.ScrollToEnd(ui.Smooth)
					}
				})
			case "request_result":
				a.requestResult(row, w, session, item, tail.receiptProgress(row, item.ID))
			case "user":
				runs := mobileclient.SentMessageRuns(item)
				content := ui.Column(row.Key("child-input-arrival")).FillWidth()
				if item.ID == inputState.presented && !row.Preferences().ReduceMotion {
					progress := min(1, float32(row.Now().Sub(inputState.presentedAt))/float32(160*time.Millisecond))
					if progress < 1 {
						content.Transform(ui.Translation(0, 8*(1-ui.EaseOut(max(0, progress)))))
						row.AnimationFrame()
					}
				}
				content.Children(func() { a.transcriptUserBubble(row, w, session, item, runs, nil, nil) })
				userMessageFooter(row, sentMessageText(runs), item.SentAt, nil, false, false, false)
			case "assistant":
				var embedDelta float32
				a.drawTranscriptMarkdown(row, w, session, float32(a.model.Preferences.ReplySize), nil, func(body *ui.Context, options *markdown.Options) {
					tail.draw(body, item, options, &list, &embedDelta)
				}, &embedDelta)
				a.contentParts(row, w, session, item.Parts)
				if text := replies[item.ID]; text != "" && !tail.holdReceipt(row, item.TurnID) {
					messageFooter(row, false).Margin(transcriptItemGap, 0, 0).Children(func() {
						footerActions(row, false, false, copyFooterAction("复制回复", text))
					})
				}
			default:
				a.transcriptMarkdown(row, w, session, item.Text, float32(a.model.Preferences.ReplySize), nil)
				a.contentParts(row, w, session, item.Parts)
			}
		}
		actions := func(c *ui.Context) {
			a.requestPanels(c, w, sid, current.ID, false)
			inputState.view(a, c.Key("child-input"), w, source, current)
			if current.Active() && current.CanStop && a.model.Connected {
				action(c, "停止任务", func() {
					a.pageJob(w, &req, "session.work.stop", protocol.WorkQuery{SessionID: sid, ID: work.ID}, nil)
				}).Disabled(req.busy)
			}
			req.status(c)
		}
		footer := func(c *ui.Context, latest func(), output string) {
			a.workDetailActions(c.Key("work-actions"), w, source, current, latest, output)
			read.status(c)
			if read.err != "" {
				action(c, "重新读取", func() { load(0) }).Disabled(!a.model.Connected)
			}
			actions(c)
		}
		if current.Kind == "background" {
			output := mobileclient.BackgroundWorkOutput(items, current.Latest)
			text := output
			if current.Brief != "" {
				text = "$ " + current.Brief
				if output != "" {
					text += "\n" + output
				}
			}
			if !terminal.scroll.Scrolling() && terminal.scroll.Y != terminalLastScroll {
				terminalFollowing = terminal.scroll.Y >= terminal.scroll.MaxY-1
			}
			follow := text != terminalText && terminalFollowing
			terminalText = text
			ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
				mobilePageContent(c, pageInset).Grow(1).Basis(0).MinHeight(0).Children(func() {
					header(c)
					view := codeViewportSurface(c, "终端输出", text, 0, nil, nil, &terminal, darkColors.inset, darkColors.ink, a.codeFont(), captionTextSize)
					view.DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
						terminalLastScroll = terminal.scroll.Y
						if follow && bounds.H > 0 && terminal.scroll.MaxY > terminal.scroll.Y {
							terminal.scroll.ScrollTo(terminal.scroll.X, terminal.scroll.MaxY, mobileOutputFollowMotion(c))
							c.Services().Invalidate()
						}
					})
					var latest func()
					if terminal.scroll.Y < terminal.scroll.MaxY-1 {
						latest = func() {
							terminalFollowing = true
							terminal.scroll.ScrollTo(terminal.scroll.X, terminal.scroll.MaxY, workScrollMotion(c))
						}
					}
					footer(c, latest, output)
				})
			})
			return
		}
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			if conversation {
				workHistoryList(c, &list, d.History, items, &read, a.model.Connected, load, header, entry)
			} else {
				ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
					mobilePageContent(c, 0).Children(func() {
						header(c)
						if !read.busy && current.Latest != "" {
							a.transcriptMarkdown(c, w, session, current.Latest, float32(a.model.Preferences.ReplySize), nil)
						}
					})
				})
			}
			mobileContent(c, conversationComposerWidth).Padding(4, conversationComposerInset, composerBottomGap).Gap(panelContentGap).
				Children(func() {
					var latest func()
					if conversation && !list.FollowingEnd() {
						latest = func() { list.ScrollToEnd(workScrollMotion(c)) }
					}
					footer(c, latest, "")
				})
		})
	}
	title := work.Title
	subtitle := strings.Trim(a.deviceName()+" · "+source.Title, " ·")
	if work.Kind == "background" {
		subtitle = a.conversationScope(source)
		if title == "" {
			title = "后台任务"
		}
	} else if title == "" {
		title = "子 Agent"
	}
	w = a.page(title, draw)
	w.OnClosed(cancel)
	if !a.preview {
		a.model.BeginWorkRead(sid, work.ID)
		w.OnClosed(func() { model.ReleaseWorkHistory(key) })
	}
	a.pushNativePage(parent, w, nativePageOptions{Title: title, Subtitle: subtitle})
}

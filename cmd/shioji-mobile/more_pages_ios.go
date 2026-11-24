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
					mobilePlanStep(c.Key(i), step, a.model.Connected)
				}
				if d.Plan.Truncated {
					mobileMessage(c, "任务内容过长，已截断显示")
				}
			}
			mobileGroupHeading(c, "本轮活动")
			a.workRows(c.Key("work-rows"), w, sid, "")
		})
	})
	a.pushNative(parent, w)
}

func mobilePlanStep(c *ui.Context, step domain.PlanStep, connected bool) {
	p := palette(c)
	font := mobileStatusFont(c, captionTextSize)
	status, ink := "待处理", p.secondary
	running := step.Status == "running" || step.Status == "in_progress"
	if running {
		status, ink = "进行中", p.ink
	} else if step.Status == "completed" {
		status = "已完成"
	}
	ui.Row(c).FillWidth().Padding(8, 0).Gap(8).AlignItems(ui.Baseline).Children(func() {
		switch {
		case running:
			transcriptStatusDot(c, step.Text, font, p.active, connected)
		case step.Status == "completed":
			transcriptSymbol(c, checkSymbol, step.Text, font, 14, p.secondary)
		default:
			transcriptStatusDot(c, step.Text, font, p.secondary, false)
		}
		text := label(c, step.Text, captionTextSize, ink).Value(status).Grow(1).MinWidth(0).
			FixedLineHeight(mobileStatusLineHeight(font))
		if running && connected {
			mobileShimmeredText(c, text)
		}
	})
}

func (a *mobileApp) openWorkList(parent *mygo.Window, sid, kind, title string) {
	var w *mygo.Window
	w = a.page(title, func(c *ui.Context) {
		mobilePageBody(c, func() { a.workRows(c, w, sid, kind) })
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) workRows(c *ui.Context, parent *mygo.Window, sid, kind string) {
	detail := a.model.Details[sid]
	found := false
	for _, work := range domain.SessionWorks(detail) {
		if kind != "" && work.Kind != kind {
			continue
		}
		if !found && detail.WorksTruncated {
			mobileMessage(c, "仅显示最近的工作记录")
		}
		found = true
		a.workRow(c.Key(work.ID), parent, sid, detail.Requests, work)
	}
	if !found {
		message := "暂无"
		switch kind {
		case "agent":
			message = "暂无子 Agent"
		case "background":
			message = "暂无后台任务"
		}
		mobileMessage(c, message)
	}
}

func (a *mobileApp) workRow(c *ui.Context, parent *mygo.Window, sid string, requests []domain.PendingRequest, work domain.WorkActivity) {
	p := palette(c)
	title, _, _ := strings.Cut(work.Title, "\n")
	preview, status := work.Latest, workStateName(work.State)
	active := a.model.Connected && work.Active()
	if !a.model.Connected && work.Active() {
		status = workStateName("unknown")
	}
	if work.Kind == "background" {
		preview = ""
	}
	var requestIcon *ui.SVG
	if a.model.Connected && work.State != "unknown" && work.State != "stopped" && work.State != "failed" {
		for _, request := range requests {
			if request.WorkID != work.ID || !request.State.AwaitingResolution() {
				continue
			}
			preview = request.Text
			if len(request.Questions) > 0 {
				preview = request.Questions[0].Text
			}
			requestIcon, status = conversationsSymbol, "等待回答"
			if request.Kind == "approval" || request.Kind == "permissions" {
				requestIcon, status = permissionSymbol, "等待授权"
			}
			break
		}
	}
	preview, _, _ = strings.Cut(strings.TrimSpace(preview), "\n")
	duration := ""
	if work.DurationMS != nil {
		duration = mobileElapsed(*work.DurationMS)
	} else if work.StartedAt != nil && !work.StartedAt.IsZero() {
		if work.CompletedAt != nil {
			duration = mobileElapsed(work.CompletedAt.Sub(*work.StartedAt).Milliseconds())
		} else if active {
			elapsed := max(0, c.Now().Sub(*work.StartedAt))
			duration = mobileElapsed(elapsed.Milliseconds())
			c.After(time.Second - elapsed%time.Second)
		}
	}
	row := mobileButton(c, work.Title, func() { a.openWork(parent, sid, work) }).FillWidth().MinWidth(0).
		PaddingY(8).AlignItems(ui.Center).Value(status)
	if preview != "" {
		row.Description(preview)
	}
	row.Children(func() {
		font := mobileStatusFont(c, captionTextSize)
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Baseline).PassThrough().Children(func() {
			switch {
			case requestIcon != nil:
				transcriptSymbol(c, requestIcon, title, font, 14, p.warning)
			case work.State == "failed":
				transcriptSymbol(c, closeSymbol, title, font, 14, p.diffRemoved)
			case work.State == "stopped":
				transcriptSymbol(c, stopSymbol, title, font, 14, p.secondary)
			case work.State == "completed":
				transcriptSymbol(c, checkSymbol, title, font, 14, p.secondary)
			case active:
				transcriptStatusDot(c, title, font, p.active, true)
			default:
				transcriptStatusDot(c, title, font, p.secondary, false)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).PassThrough().Children(func() {
				metadata := ui.Row(c.Key("metadata")).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Baseline).PassThrough()
				titleInk, durationInk := p.ink, p.secondary
				if active && work.State != "waiting" && requestIcon == nil {
					if mobileTextSweep(c, metadata) {
						titleInk, durationInk = mobileSweepMask, mobileSweepMask
					}
				}
				metadata.Children(func() {
					oneLine(c, title, captionTextSize, titleInk).Grow(1).FixedLineHeight(mobileStatusLineHeight(font)).PassThrough()
					if duration != "" {
						oneLine(c, strings.ReplaceAll(duration, " ", ""), captionTextSize-1, durationInk).
							Font("Menlo").FontFeatures("tnum").Shrink(0).PassThrough()
					}
				})
				stateInk := p.secondary
				if work.State == "failed" {
					stateInk = p.diffRemoved
				} else if requestIcon != nil {
					stateInk = p.warning
				}
				if status != "" || preview != "" {
					ui.Row(c.Key("work-status")).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Baseline).PassThrough().Children(func() {
						if status != "" {
							oneLine(c, status, captionTextSize-1, stateInk).Shrink(0).PassThrough()
						}
						if status != "" && preview != "" {
							oneLine(c, "·", captionTextSize-1, p.secondary).Shrink(0).PassThrough()
						}
						if preview != "" {
							oneLine(c, preview, captionTextSize-1, p.secondary).MinWidth(0).Shrink(1).PassThrough()
						}
					})
				}
			})
			transcriptSymbol(c, rightSymbol, title, font, 12, p.secondary)
		})
	})
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
						sweep.caption(c.Key("duration"), mobileElapsed(*current.DurationMS), ui.ContentTransition{})
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
				userMessageFooter(row, sentMessageText(runs), item.SentAt, nil, false, false, 1)
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

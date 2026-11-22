package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) homeView(c *ui.Context) {
	p := palette(c)
	bg := p.canvas
	if a.wide {
		bg = p.panel
	}
	root := c.Root().Background(bg)
	if a.wide {
		root.BorderEndWidth(1).BorderColor(p.line)
	}
	ui.Column(c).Fill().Background(bg).Children(func() {
		a.sessionList(c)
		if !a.searching && !a.prominentCompose {
			bottom, gap := float32(8), float32(4)
			if a.wide {
				bottom, gap = 0, 10
			}
			ui.Row(c).Padding(0, 12, bottom).Gap(gap).AlignItems(ui.Center).Children(func() {
				if a.wide {
					tabs := ui.Row(c).Grow(1).MinWidth(0).Height(max(50, mobileTapSize(c)+6)).Padding(3).Gap(2)
					floatingSurface(c, tabs, 25).Children(func() {
						for index, item := range []struct {
							title string
							icon  *ui.SVG
						}{
							{"会话", conversationsSymbol}, {"设备", deviceSymbol}, {"设置", gearSymbol},
						} {
							row := c.Key(index)
							selected := p.canvas.Alpha(0)
							if index == 0 {
								selected = p.selected
							}
							ui.ButtonBase(row).Grow(1).Basis(0).MinWidth(mobileTapSize(c)).MinHeight(mobileTapSize(c)).Radius(24).
								AlignItems(ui.Center).Justify(ui.Center).Background(selected).
								Label(item.title).Role(ui.RoleButton).OnClick(func() {
								if index != 0 {
									a.callUIKit("selectTab", map[string]int{"index": index}, nil)
								}
							}).Children(func() {
								ui.Column(row).AlignItems(ui.Center).Gap(1).PassThrough().Children(func() {
									ui.Icon(row, item.icon).Size(17, 17).TextColor(p.ink).PassThrough()
									oneLine(row, item.title, 10, p.ink)
								})
							})
						}
					})
				} else {
					ui.Spacer(c)
				}
				size := mobileTapSize(c)
				if a.wide {
					size = max(49, mobileTapSize(c))
				}
				floatingSurface(c, iconButton(c, "新建会话", composeSymbol, toolbarIconSize,
					func() { a.openDraft(a.home) }).Size(size, size), size/2)
			})
		}
	})
}

func (a *mobileApp) detailView(c *ui.Context) {
	p := palette(c)
	findShortcut := a.takeFindShortcut(a.detail)
	if a.wide && a.model.SessionID != "" {
		const inspectorWidth = 315
		current := a.currentSplitReview()
		showLeft := !current.right
		hasParallel := a.parallelDevice != nil || a.parallel != "" && a.parallel != a.model.SessionID
		showRight := hasParallel && (current.state == nil || current.right)
		if findShortcut {
			if showRight && (!showLeft || a.parallelFindHandle.FocusWithin(c)) {
				if pane := a.parallelDevice; pane != nil {
					pane.openConversationFind(a.detail, pane.session(), true)
				} else {
					a.openConversationFind(a.detail, a.sessionByID(a.parallel), true)
				}
			} else if showLeft {
				a.openConversationFind(a.detail, a.session(), false)
			}
		}
		ui.Row(c).Fill().Background(p.canvas).Children(func() {
			splitColumn(c.Key("primary-transcript"), showLeft, func() {
				a.conversation(c, a.detail, true)
			})
			if hasParallel {
				splitColumn(c.Key("parallel-transcript"), showRight, func() { a.parallelContent(c) })
			}
			target := float32(0)
			if current.state != nil {
				target = inspectorWidth
			}
			panel := ui.Column(c).Clip().Shrink(0).HeightPercent(100).BorderWidth(0, 0, 0, 1).BorderColor(p.line)
			width := panel.AnimateTo("changes-panel-width", target, mobileLayoutMotion)
			panel.Width(width)
			visible := ui.Local(panel, "changes-panel", func() splitReview { return splitReview{} })
			if current.state != nil {
				*visible = current
			}
			if shown := *visible; width > 0 && shown.state != nil {
				panel.Disabled(target == 0).Children(func() {
					ui.Column(c).Width(inspectorWidth).HeightPercent(100).Children(func() {
						shown.owner.reviewContent(c, shown.state, func() {
							if shown.owner.review == shown.state {
								shown.owner.replaceReview(nil)
								a.refresh()
							}
						})
					})
				})
			} else {
				*visible = splitReview{}
			}
		})
		return
	}
	ui.Column(c).Fill().Background(p.canvas).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		label(c, "选择会话", 16, p.secondary)
	})
}

func splitColumn(c *ui.Context, visible bool, render func()) {
	pane := ui.Column(c).Basis(0).MinWidth(0).HeightPercent(100).Clip()
	target := float32(0)
	if visible {
		target = 1
	}
	weight := pane.AnimateTo("pane-width", target, mobileLayoutMotion)
	pane.Grow(weight)
	if weight > 0 {
		pane.Disabled(!visible).Children(render)
	}
}

func (a *mobileApp) conversation(c *ui.Context, parent *mygo.Window, split bool) {
	p := palette(c)
	s := a.session()
	if !split && a.takeFindShortcut(parent) {
		a.openConversationFind(parent, s, false)
	}
	detail := a.model.Details[s.ID]
	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		if split {
			ui.Row(c).Gap(16).AlignItems(ui.Center).MinHeight(55).Padding(2, 16, 4).Children(func() {
				toolbarButton(c, "切换侧栏", panelSymbol, func() {
					a.callUIKit("toggleSidebar", nil, nil)
				})
				ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Baseline).Gap(8).Children(func() {
					oneLine(c.Key("conversation-title"), s.Title, conversationTitle, p.ink).FontWeight(650).Grow(1).Shrink(1)
					if summary := a.conversationHeaderScope(s); summary != "" {
						ui.ButtonBase(c.Key("conversation-scope")).MinWidth(0).MaxWidthPercent(48).MinHeight(mobileTapSize(c)).
							AlignItems(ui.Center).Justify(ui.End).Gap(4).Shrink(1).
							Role(ui.RoleButton).Label("打开当前设备详情").Value(summary).
							OnClick(func() { a.openDevice(parent) }).Children(func() {
							_, glyph := mobileDeviceIcon(a.splitRoot().savedDevice(a.endpoint.DeviceID).Icon)
							alignedSymbol(c, glyph, summary, mobileStatusFont(c, captionTextSize), 11, p.secondary)
							oneLine(c, summary, captionTextSize, p.secondary).Shrink(1)
						})
					}
				})
				toolbarButton(c, "会话活动", moreSymbol, func() { a.openActivity(parent, s.ID) })
			})
		}
		a.connectionNotice(c.Key("connection"), parent)
		find := a.conversationFind(c, parent, s, detail, false, func(mods ui.Modifiers, key ui.Key) bool {
			rightVisible := split && a.review == nil && (a.parallelDevice != nil || a.parallel != "")
			return (!rightVisible || !a.parallelFindHandle.FocusWithin(c)) && c.Shortcut(mods, key)
		})
		var draft *mobileDraftEditor
		if find == nil && !s.Archived && !a.workspace(s.WorkspaceID).Archived {
			draft = a.bindDraftEditor(c, a.draftTarget())
		}
		a.renderTranscript(c, parent, s, detail, true, find, nil, draft)
		a.historyStatus(c, parent, s)
		readingImport := a.importedHistoryFooter(c, parent, s, detail)
		if !readingImport && draft != nil {
			a.conversationEditor = draft
			a.drawComposer(c, parent, s, draft, false, false)
		}
	})
}

func (a *mobileApp) renderTranscript(c *ui.Context, parent *mygo.Window, session domain.Session, detail domain.SessionDetail,
	interactive bool, find *mobileConversationFind, preview *archiveTranscript, draft *mobileDraftEditor) {
	p := palette(c)
	sid := session.ID
	historyActions := interactive || preview != nil || parent != nil && parent == a.detail
	detachedEdit := historyActions && preview == nil && a.detachedHistoryEdit(sid).ItemID != ""
	var stop *mobileSessionStop
	var state *ui.ListState
	var timeline *mobileToolTimeline
	var compaction *mobileCompaction
	if preview != nil {
		state, timeline, compaction = &preview.list, &preview.timeline, &preview.compaction
	} else {
		stop = a.sessionStop(session)
		detail = stop.transcript(detail)
		state = a.history[sid]
		if state == nil {
			state = &ui.ListState{FollowEnd: true}
			a.history[sid] = state
		}
		timeline, compaction = a.toolTimeline(session), a.compaction(session)
	}
	if jump := a.historyJump; interactive && jump.session == sid && jump.thread == session.ThreadID && jump.center && (a.model.Connected || jump.workSource != nil) {
		if !jump.ready || jump.err != "" {
			mobileContent(c.Key("locating"), contentMaxWidth).Grow(1).Padding(pageInset).Children(func() {
				if jump.err == "" {
					mobileMessage(c, "正在定位消息…")
					return
				}
				mobileMessage(c, jump.err)
				action(c, "打开最新消息", func() {
					a.historyJump = transcriptJump{}
					if a.controller != nil {
						a.controller.Select(sid)
					}
					a.refresh()
				}).AlignSelf(ui.Start).PaddingX(0)
			})
			return
		}
	}
	workAnchor := ""
	if jump := a.historyJump; interactive && jump.session == sid && jump.thread == session.ThreadID && jump.workSource != nil && jump.ready {
		workAnchor = mobileclient.WorkSourceAnchor(detail, *jump.workSource)
		if workAnchor == "" {
			c.AddToast(ui.Toast{ID: "missing-work-source", Title: "该任务在会话中没有可定位的消息"})
			a.historyJump = transcriptJump{}
		}
	}
	items := mobileclient.ConversationItems(detail)
	older := detail.History != nil && detail.History.Older
	newer := detail.History != nil && detail.History.Newer
	items = compaction.project(items, session, !newer && detail.HasLoadedHistory())
	var questionStatuses map[string]string
	if slices.ContainsFunc(items, func(item domain.Item) bool { return len(item.Questions) != 0 }) {
		questionStatuses = asyncQuestionStatuses(items, a.model, sid, interactive && preview == nil)
	}
	liveCompaction := a.currentLiveCompaction(session, detail, items)
	transcript := mobileclient.TranscriptRows(items, detail.Turns, !newer && (interactive || preview != nil) && liveCompaction == "")
	groups := timeline.Groups(items, transcript, detail, !newer && a.model.Connected, a.model.Preferences.CollapseLatestTools)
	if preview != nil {
		if older {
			first, last := state.Visible()
			before, _ := state.ScrollEdges()
			if len(transcript) == 0 || first == 0 && last >= 0 && before < .5 && state.AtEnd() {
				preview.fillEarlier()
			}
		}
		older, newer = false, false
	}
	if len(transcript) == 0 && !older && !newer {
		ui.Column(c).Grow(1).Children(func() {
			if preview != nil {
				zoom := c.Zoom()
				label(c, "暂无会话记录", controlTextSize, p.secondary).Padding(4, pageInset, transcriptItemGap).Role(ui.RoleStatus).
					DrawOver(func(_ *ui.Painter, r ui.Rect) { preview.fitHeight((r.Y + r.H) * zoom) })
				return
			}
			read := a.model.HistoryReads[sid]
			if read != nil && read.Error != "" {
				label(c, read.Error, captionTextSize, p.warning).Padding(16, 16, 0)
				if interactive && a.controller != nil && a.model.Connected {
					action(c, "重试读取历史", func() { a.controller.Select(sid) }).AlignSelf(ui.Start).Margin(0, 16)
				}
			} else if !detail.HasLoadedHistory() && !a.preview && a.model.Connected {
				label(c, "正在读取会话…", 13, p.secondary).Padding(16)
			}
		})
		return
	}
	frame := ui.Column(c.Key("transcript-frame:"+sid)).Grow(1).Basis(0).MinHeight(0).Padding(2, 0, 0)
	scope := replyHistoryScope{session: sid, thread: session.ThreadID, connection: a.model.ConnectionEpoch,
		snapshot: detail.Sequence, activation: a.replyActivation}
	// UIKit keeps a pushed page mounted while another view is in front.
	front := parent == nil || parent != a.chat || c.WindowActive()
	reading := a.model.HistoryReads[sid]
	canAnimate := find == nil && !session.Archived && !session.ReadOnlyHistory() &&
		a.model.Connected && detail.HasLoadedHistory() && (detail.History == nil || !detail.History.Invalidated && !newer) &&
		(reading == nil || !reading.Pending) && state.FollowingEnd() && front && !c.Preferences().ReduceMotion
	canReveal := interactive && canAnimate
	steer := ui.Local(frame, "steer-footer", func() steerFooterHandoff { return steerFooterHandoff{} })
	transcript = steer.project(c, scope, transcript, items, a.model.UncertainSubmissions, canReveal)
	turns := make(map[string]domain.Turn, len(detail.Turns))
	for _, turn := range detail.Turns {
		turns[turn.ID] = turn
	}
	var workTurn domain.Turn
	var workCaption string
	var workStart *time.Time
	var workLive bool
	for _, turn := range slices.Backward(detail.Turns) {
		if turn.Status.Active() {
			workTurn = turn
			workCaption, workStart, workLive = a.liveTurnStatus(sid, turn, detail.Requests, items)
			break
		}
	}
	if liveCompaction != "" {
		workCaption = "正在压缩上下文"
	}
	var activeToolFooter, toolsShowProgress bool
	if n := len(transcript); n > 1 && workTurn.ID != "" && transcript[n-1].Footer == workTurn.ID {
		tail := transcript[n-2]
		for first, group := range groups {
			if items[first].TurnID != workTurn.ID {
				continue
			}
			if workLive && liveCompaction == "" && group.Live {
				activeToolFooter, toolsShowProgress = true, true
				break
			}
			if tail.Footer == "" && tail.First == first {
				activeToolFooter = true
			}
		}
	}
	var replyOrder int64
	var lastReply domain.Item
	for _, item := range items {
		replyOrder = max(replyOrder, item.Order)
		switch item.Kind {
		case "assistant":
			lastReply = item
		case "user", "command":
			lastReply = domain.Item{}
		}
	}
	offset, rows := 0, len(transcript)
	if older {
		offset, rows = 1, rows+1
	}
	if newer {
		rows++
	}
	state.Reveals = state.Reveals[:0]
	state.FollowAnimation = nil
	if draft != nil {
		draft.sendScroll.reveal(c, frame, state, a.model, items, transcript, offset, canAnimate)
	}
	state.SkipAnchor = func(index int) bool {
		return index < offset || index-offset >= len(transcript) || transcript[index-offset].Footer != ""
	}
	state.Key = func(index int) any {
		switch {
		case index < offset:
			return transcriptOlder
		case index-offset < len(transcript):
			row := transcript[index-offset]
			if row.Footer != "" {
				if row.Receipt.AfterInput != "" {
					return transcriptReplyKey(row.Receipt.AfterInput)
				}
				return transcriptTurnKey(row.Footer)
			}
			if group, ok := groups[row.First]; ok {
				return group.Key
			}
			return compaction.rowKey(items[row.First].ID)
		}
		return transcriptNewer
	}
	if interactive && detail.History != nil && !detail.History.Invalidated {
		first, last := state.Visible()
		if state.FollowingEnd() && !newer {
			delete(a.model.HistoryAnchors, sid)
		} else {
			for index := max(first, offset); index <= last && index-offset < len(transcript); index++ {
				if row := transcript[index-offset]; row.Footer == "" {
					item := items[row.First]
					if slices.ContainsFunc(detail.Items, func(source domain.Item) bool { return source.ID == item.ID }) {
						a.model.HistoryAnchors[sid] = item.ID
						break
					}
				}
			}
		}
	}
	target, reveal := "", false
	if jump := a.historyJump; interactive && jump.session == sid {
		if jump.thread != session.ThreadID {
			a.historyJump = transcriptJump{}
		} else if jump.center {
			if jump.ready && jump.err == "" {
				target, reveal = jump.item, true
				if jump.workSource != nil {
					target = workAnchor
				}
			}
			a.historyJump = transcriptJump{}
		} else {
			for index, row := range transcript {
				if row.Footer == "" && items[row.First].ID == jump.item {
					state.KeepRowWithinContent(index+offset, 10)
					a.model.HistoryAnchors[sid] = jump.item
					break
				}
			}
			a.historyJump = transcriptJump{}
		}
	}
	if find != nil && find.run.mark != nil {
		reveal = find.run.mark.reveal
		if reveal {
			target = find.run.mark.item
		}
	}
	if reveal {
		for index, entry := range transcript {
			if entry.Footer != "" {
				continue
			}
			matched := slices.IndexFunc(items[entry.First:entry.End], func(item domain.Item) bool { return item.ID == target })
			if matched < 0 {
				continue
			}
			if group, ok := groups[entry.First]; ok && !group.Open {
				timeline.Toggle(group.Key)
				group.Open = true
				groups[entry.First] = group
			}
			switch items[entry.First+matched].Kind {
			case "tool", "activity", "reasoning":
				timeline.bodies[target] = new(true)
			}
			state.ScrollTo(index+offset, ui.Center)
			if find != nil {
				find.run.mark.row = index + offset
				find.run.mark.reveal = false
			}
			break
		}
		if find != nil {
			target, reveal = "", false
		}
	}
	prefetch := ui.Local(frame, "history-prefetch", func() transcriptPrefetch { return transcriptPrefetch{} })
	current := func() bool {
		if interactive || a.parallelOwner != nil {
			return a.model.SessionID == sid
		}
		return parent == a.detail && a.parallel == sid && a.review == nil
	}
	tail := ui.Local(frame, "reply-tail", func() replyTail { return replyTail{} })
	readVisit := ui.Local(frame, "completion-read", func() [3]string { return [3]string{} })
	canReadReply := preview == nil && find == nil && parent != nil && c.WindowActive() && mygo.App.IsActive() &&
		!a.preview && a.model.Connected && detail.HasLoadedHistory() && !newer && !session.Archived && !a.workspace(session.WorkspaceID).Archived
	if !canReadReply {
		*readVisit = [3]string{}
	}
	tail.observe(c.Now(), scope, replyOrder, lastReply,
		canReveal && turns[lastReply.TurnID].Status != domain.ExecutionInterrupted)
	frame.Children(func() {
		list := ui.List(c.Key(sid), state, rows, func(index int) {
			if index < offset {
				a.transcriptHistoryControl(c, parent, detail, prefetch, transcriptOlder, current)
				return
			}
			index -= offset
			if index == len(transcript) {
				a.transcriptHistoryControl(c, parent, detail, prefetch, transcriptNewer, current)
				return
			}
			entry := transcript[index]
			previewTail := preview != nil && index+1 == len(transcript)
			if entry.Footer != "" {
				row := c.Key(state.Key(index + offset))
				turn := turns[entry.Footer]
				receipt := entry.Receipt
				bottom := float32(transcriptItemGap / 2)
				if previewTail {
					bottom = transcriptItemGap
				} else if receipt.BeforeInput != "" || index+1 < len(transcript) && transcript[index+1].Footer == "" && items[transcript[index+1].First].Kind == "user" {
					bottom = transcriptTurnGap - transcriptItemGap/2
				}
				footer := mobileContent(row, contentMaxWidth).Padding(transcriptItemGap/2, pageInset, bottom)
				if previewTail {
					preview.fitTail(c, footer)
				}
				intermediate := receipt.BeforeInput != ""
				hold := !intermediate && turn.Status == domain.ExecutionCompleted && tail.holdReceipt(row, turn.ID)
				if !intermediate && canReadReply && !hold {
					a.observeReadCompletion(c, footer, state, session, turn, readVisit)
				}
				footer.Children(func() {
					if intermediate {
						body := row.Key("intermediate-summary")
						summary := ui.Column(body).FillWidth()
						if steer.insertion == receipt.BeforeInput {
							summary.Transition(ui.ElementTransition{
								Duration: mobileReplyFade, Ease: ui.EaseOut, Enter: &ui.Motion{Opacity: 0},
							})
						}
						summary.Children(func() {
							a.replyFooter(body, parent, sid, turn, receipt, false, "", previewTail)
						})
						return
					}
					render := func(body *ui.Context) {
						a.turnReceipt(body, parent, sid, turn, receipt, detail.Requests, items,
							historyActions && (!stop.pending() || turn.ID != stop.turn), hold, activeToolFooter && turn.ID == workTurn.ID, toolsShowProgress && turn.ID == workTurn.ID, previewTail)
					}
					if canReveal && turn.Status.Active() && !toolsShowProgress && !activeToolFooter {
						body := row.Key("running-footer")
						ui.Column(body).FillWidth().Transition(ui.ElementTransition{
							Duration: mobileReplyFade, Ease: ui.EaseOut, Exit: &ui.Motion{Opacity: 0},
						}).DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
							if bounds.H > 0 {
								steer.observeRunning(entry)
							}
						}).Children(func() { render(body) })
						return
					}
					render(row)
				})
				return
			}
			first, end := entry.First, entry.End
			item := items[first]
			row := c.Key(state.Key(index + offset))
			bottom := float32(transcriptItemGap / 2)
			if previewTail {
				bottom = transcriptItemGap
			} else if end < len(items) && items[end].Kind == "user" && item.Kind != "user" &&
				index+1 < len(transcript) && transcript[index+1].Footer == "" {
				bottom = transcriptTurnGap - transcriptItemGap/2
			}
			content := mobileContent(row, contentMaxWidth).Padding(transcriptItemGap/2, pageInset, bottom)
			if previewTail {
				preview.fitTail(c, content)
			}
			content.Children(func() {
				var decorate func(domain.Item) *markdown.RangeDecoration
				if find != nil && find.run.mark != nil {
					decorate = func(item domain.Item) *markdown.RangeDecoration {
						return find.decoration(row, item, state, content.Bounds)
					}
				}
				if item.Presentation == domain.GoalContinuationPresentation {
					goalContinuation(row, item.Name)
					return
				}
				if item.Presentation == "compaction" {
					live := liveCompaction != "" && item.ID == liveCompaction
					if action := compaction.action(session); a.model.Connected && action != nil && action.State.Active() && !item.Final &&
						item.ID == "context-compaction:"+action.OperationID {
						live = true
					}
					turn := turns[item.TurnID]
					if turn.StartedAt == nil && live && !compaction.started.IsZero() {
						turn.StartedAt = &compaction.started
					}
					a.compactionRow(row, session, item, live, turn, interactive, compaction)
					return
				}
				if group, ok := groups[first]; ok {
					a.toolGroup(row, parent, session, items[first:end], group, timeline, state, index+offset, workCaption, workStart, workLive && liveCompaction == "", target, reveal, decorate)
					return
				}
				var mark *markdown.RangeDecoration
				if decorate != nil {
					mark = decorate(item)
				}
				switch item.Kind {
				case "user", "command":
					if item.Kind == "user" && historyActions && preview == nil && a.historyEditor(row, parent, session, item) {
						return
					}
					runs := mobileclient.SentMessageRuns(item)
					message := sentMessageText(runs)
					if item.Kind == "command" && item.Presentation == "compact" {
						message = "压缩上下文"
						compactCommandChip(row, message)
					} else {
						if item.Kind == "command" && item.Name != "" {
							message = item.Name
						}
						a.transcriptUserBubble(row, parent, session, item, runs, mark)
					}
					var edit func()
					canEdit := false
					source := a.sessionByID(sid)
					if item.Kind == "user" && historyActions && !source.Archived {
						canEdit = item.Status == "sent" && item.TurnID != "" && a.historyActionAvailable(source) &&
							a.model.SessionReadiness(source).Capabilities.Restart && a.model.HistoryActions[sid].OperationID == "" && !detachedEdit
						edit = func() {
							if !a.visibleLinkSource(parent, source) || a.detachedHistoryEdit(sid).ItemID != "" {
								return
							}
							if err := a.model.BeginSessionHistoryEdit(sid, item); err != nil {
								a.model.Error = err.Error()
							} else {
								state.KeepRowWithinContent(index+offset, content.Bounds().Y-state.Bounds(c).Y)
								a.save()
							}
						}
					}
					userMessageFooter(row, message, item.SentAt, edit, canEdit, previewTail)
				case "assistant":
					turn, hasTurn := turns[item.TurnID]
					if len(item.Questions) != 0 {
						a.asyncQuestionMessage(row, parent, session, item, questionStatuses, interactive && preview == nil)
					} else if item.Text != "" {
						a.drawTranscriptMarkdown(row, parent, session, float32(a.model.Preferences.ReplySize), mark, func(body *ui.Context, options *markdown.Options) {
							tail.draw(body, item, options)
						})
					}
					a.contentParts(row, parent, session, item.Parts)
					if item.Final && !hasTurn {
						// A reply from old history can have a message but no turn
						// metadata. Its actions and unknown fields are still useful.
						turn.ID = item.TurnID
						footer := row.Key("reply-footer")
						ui.Column(footer).FillWidth().Margin(transcriptItemGap, 0, 0).Children(func() {
							if item.ID == tail.replyID && tail.holdReceipt(footer, turn.ID) {
								mobileReplyProgress(footer, "正在回复", nil, nil, true)
							} else {
								receipt := mobileclient.ReplyReceipt{Reply: []string{item.Text}}
								a.replyFooter(footer, parent, sid, turn, receipt, historyActions, "", previewTail)
							}
						})
					}
				case "request":
					a.requestSource(row, parent, sid, item, detail.Requests, historyActions, nil)
				case "request_result":
					a.requestResult(row, parent, session, item)
				case "activity", "reasoning":
					var body *bool
					if target != "" || mark != nil {
						body = timeline.bodies[item.ID]
					}
					a.toolItem(row, parent, session, item, body, mark, false)
				default:
					if item.Text != "" {
						label(row, item.Text, 13, p.secondary).Selectable()
					}
					a.contentParts(row, parent, session, item.Parts)
				}
			})
		}).Grow(1).Basis(0).MinHeight(0)
		a.prefetchTranscript(c, list, state, prefetch, parent, detail, current, parent != nil && preview == nil && historyActions && find == nil && !reveal)
		if interactive && find == nil && (newer || !state.AtEnd() && !state.FollowingEnd()) && a.model.HistoryEdits[sid].ItemID == "" {
			if !workLive {
				workCaption = "最新消息"
				if workTurn.ID != "" && !a.model.Connected {
					workCaption = "状态待确认"
				}
			}
			a.transcriptLatest(c.Key("return-latest"), sid, detail, state, prefetch, newer, workCaption, workStart, workLive)
		}
	})
}

func (a *mobileApp) drawComposer(c *ui.Context, parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, home, right bool) {
	m := a.model
	p := palette(c)
	targetRef := editor.target
	key, sid := targetRef.key, targetRef.session
	sending, uncertain := mobileclient.SubmissionPresentation(m, targetRef.workspace, sid)
	ready := editor.readiness()
	runtimeBlocked := ready.State != domain.RuntimeReady
	var config domain.RunConfig
	if home {
		config = m.Config()
	} else {
		config = m.SessionConfig(session)
	}
	modelUnavailable := ready.ModelUnavailable(config.Model)
	_, goalCommand := domain.GoalCommand(m.Drafts[key])
	active := session.Status.Active()
	stop := a.sessionStop(session)
	stopping := stop.pending()
	connected := m.Connected && (!right || m.ConnectionEpoch != "")
	readyPhase := m.ConnectionPhase == app.ConnectionIdle || m.ConnectionPhase == app.ConnectionSwitching
	saveFailed := a.draftSaveFailed()
	queueEdit := m.ActiveQueueEdit(sid)
	queue := a.queueRequest(c, sid)
	compact := a.compaction(session)
	work := compact.action(session)
	manualCompaction := work != nil && work.State.Active()
	attachmentsReady := a.preview || a.draftAttachmentsReady(targetRef)
	canCompose := func() bool {
		return connected && readyPhase && !session.ContextChanged && !a.draftSaveFailed() && attachmentsReady && !m.HasPendingInputForSession(sid) && queueEdit.ID == "" && !editor.pending() && m.HasDraftContent(key)
	}
	canSubmit := func() bool {
		return canCompose() && ((!runtimeBlocked && !modelUnavailable) || mobileCompactCommand(m.Drafts[key]))
	}
	canSteer := func() bool {
		_, goalDraft := domain.GoalCommand(m.Drafts[key])
		if !active || stopping || manualCompaction || goalDraft || mobileCompactCommand(m.Drafts[key]) || !canCompose() ||
			!ready.Capabilities.Steer || m.ActiveTurnForSession(sid) == "" ||
			(session.Config.Model != "" && session.Config.Model != config.Model) {
			return false
		}
		// A catalog refresh can retire a model while the old process still
		// accepts input for the turn it is executing.
		if live, ok := ready.Sessions[sid]; ok && live.ID == targetRef.harness {
			return live.State == domain.RuntimeReady && live.Capabilities.Steer
		}
		return ready.State == domain.RuntimeReady && !modelUnavailable
	}
	submit := func(steer bool) {
		if !editor.current(a, parent) {
			return
		}
		if steer && canSteer() || !steer && canSubmit() {
			a.submit(steer, editor)
		}
	}
	canSaveQueue := func() bool {
		return connected && !a.draftSaveFailed() && attachmentsReady && !queue.busy && !editor.pending() &&
			m.HasDraftContent(key) && editableQueueItem(m.Details[sid].Queue, queueEdit.ID)
	}
	saveQueue := func() {
		if editor.current(a, parent) && canSaveQueue() {
			a.saveQueueEdit(parent, session, editor, queue)
		}
	}
	width, inset, topInset := float32(conversationComposerWidth), float32(conversationComposerInset), float32(4)
	if home {
		width, inset, topInset = draftComposerWidth, 12, 2
	}
	// Let scrollable panels give up height before the fixed input surface.
	mobileContent(c, width).MinHeight(0).Padding(topInset, inset, composerBottomGap).Gap(composerPanelGap).Children(func() {
		if !home {
			a.requestPanels(c, parent, sid, "", false)
			a.recoveryControls(c, parent, session, editor)
			if session.ContextChanged {
				a.branchContextNotice(c.Key("branch-context"), parent, session, editor, right, connected)
			}
			if ready.Capabilities.Compact || session.ContextUsage != nil || compact.phase == mobileCompactionUnknown {
				ui.Row(c.Key("context-controls")).AlignItems(ui.Center).Gap(4).Children(func() {
					ui.Spacer(c)
					a.compactionControl(c.Key("compaction"), session, compact)
				})
			}
			a.goalControl(c.Key("session-goal"), parent, session)
			a.queue(c, parent, session, editor, queue)
			if compact.message != "" {
				ui.Column(c.Key("compaction-notice")).Transition(mobilePanelTransition).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						label(c, compact.message, 12, p.warning).Grow(1).MinWidth(0)
						if compact.phase == mobileCompactionUnknown {
							action(c, "核对压缩结果", func() { a.queryCompaction(session, compact) }).KeepFocus().Disabled(!m.Connected).PaddingX(2)
						}
					})
				})
			}
		}
		for _, submission := range uncertain {
			a.operationReceipt(c.Key(submission.OperationID), parent, submission)
		}
		if stop != nil && stop.err != "" {
			label(c, stop.err, captionTextSize, p.warning).Role(ui.RoleStatus)
		} else if stopping && stop.turn == "" {
			mobileProgressRow(c, "已停止", false, func() {})
		}
		if len(uncertain) == 0 {
			a.composerOperationError(c.Key("operation-error"), parent, editor, !right || a.parallelOwner != nil)
		}
		if editor.attachmentError != "" {
			label(c.Key("attachment-error"), editor.attachmentError, captionTextSize, p.warning).Role(ui.RoleStatus)
		}
		if saveFailed {
			a.draftSaveRecovery(c.Key("draft-save"), parent, editor)
		}
		if connected && runtimeBlocked {
			a.composerRuntimeStatus(c.Key("runtime-status"), parent, editor, ready.State, active)
		}
		a.pathAttachmentPanel(c, parent, editor)
		a.draftReferences(c, parent, editor)
		editor.referencePanel(a, c, parent)
		expanded := editor.focused || m.HasDraftContent(key) || queueEdit.ID != "" || modelUnavailable
		surface, geometry := mobileComposerSurface(c, key, home, expanded, &editor.sendScroll)
		controlsProgress := geometry.ControlsProgress
		surface.Children(func() {
			placeholder := "发消息…"
			if home {
				harness := "Agent"
				for _, entry := range m.Readiness.Harnesses {
					if entry.ID == m.HarnessID() && entry.Name != "" {
						harness = entry.Name
						break
					}
				}
				placeholder = "与 " + harness + " 一起工作…"
			}
			ui.Column(c).Margin(geometry.TextTop, geometry.TextRight, 0, geometry.TextInset).
				Padding(mobileclient.ComposerEditorPadding, 4).Height(geometry.EditorHeight).Children(func() {
				title := "消息"
				if right {
					title = "右侧会话草稿"
				}
				editor.field(a, c, parent, title, placeholder, false, 0, func(steer bool) {
					if queueEdit.ID != "" && !steer {
						saveQueue()
					} else {
						submit(steer)
					}
				})
			})
			ui.Row(c).Absolute().Left(5).Right(5).Top(geometry.ControlsY).Height(mobileTapSize(c)).AlignItems(ui.Center).Gap(2).PassThrough().Children(func() {
				attachmentSize := float32(17)
				if home {
					attachmentSize = 12
				}
				attachmentTitle := "添加附件"
				if right {
					attachmentTitle = "添加右侧附件"
				}
				a.composerAttachmentMenu(c, parent, editor, attachmentTitle, attachmentSize)
				if !home && controlsProgress < .62 {
					ui.Spacer(c).PassThrough()
				}
				a.runControls(c, parent, editor, ready, config, controlsProgress, home, right)
				if home {
					ui.Spacer(c).PassThrough()
				}
				switch {
				case queueEdit.ID != "":
					action(c, "取消", func() { m.CancelQueueEdit(sid); a.save() }).KeepFocus().Disabled(queue.busy)
					sendButton(c, "保存待发消息", checkSymbol, home, !canSaveQueue(), saveQueue).KeepFocus()
				case mobileCompactCommand(m.Drafts[key]):
					title := "执行压缩命令"
					if right {
						title = "执行右侧压缩命令"
					}
					sendButton(c, title, sendSymbol, home, !canSubmit(), func() { submit(false) })
				case sending && connected:
					title := "正在发送"
					if right {
						title = "正在发送右侧消息"
					}
					sendButton(c, title, nil, home, true, nil)
				case active && !stopping && (saveFailed || session.ContextChanged || runtimeBlocked && !canSteer() ||
					!m.HasDraftContent(key) || session.Status == domain.ExecutionStarting && m.HasPendingInputForSession(sid)):
					title := "停止"
					if right {
						title = "停止右侧回复"
					}
					sendButton(c, title, stopSymbol, home, !connected || !a.preview && !ready.Capabilities.Interrupt, func() {
						if editor.current(a, parent) {
							a.stopSession(sid)
						}
					})
				case active && !stopping && !manualCompaction && !goalCommand && ready.Capabilities.Steer:
					title := "发送选项"
					if right {
						title = "右侧发送选项"
					}
					sendButton(c, title, sendSymbol, home, !canSubmit() && !canSteer(), nil).PopoverMenuButton(func(menu *ui.Menu) {
						if menu.Item("加入待发队列").Disabled(!canSubmit()).Chosen() {
							submit(false)
						}
						if menu.Item("介入当前轮").Disabled(!canSteer()).Chosen() {
							submit(true)
						}
					}, composerMenuAbove)
				default:
					title := "发送"
					if right {
						title = "发送右侧消息"
					}
					if active && (!stopping || m.HasDraftContent(key)) || session.Goal.Active() || len(m.Details[sid].Queue.Items) > 0 {
						title = "加入待发队列"
						if right {
							title = "加入右侧待发队列"
						}
					}
					awaitingCompaction := compact.phase == mobileCompactionSending && !active
					sendButton(c, title, sendSymbol, home, !canSubmit() || awaitingCompaction, func() { submit(false) })
				}
			})
		})
	})
}

func sendButton(c *ui.Context, title string, symbol *ui.SVG, home, disabled bool, onClick func()) ui.Element {
	p := palette(c)
	size, glyph := tapSize, float32(17)
	fill, ink := p.ink, p.canvas
	if home {
		size, glyph = 30, 14
		if disabled {
			fill, ink = p.selected, p.secondary
		}
	}
	return mobileButton(c, title, onClick).KeepFocus().Size(mobileTapSize(c), mobileTapSize(c)).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).
		Disabled(disabled).DimDisabled(!home).Children(func() {
		ui.Column(c).Size(size, size).Radius(size / 2).AlignItems(ui.Center).Justify(ui.Center).Background(fill).
			PassThrough().Children(func() {
			if symbol == nil {
				ui.Spinner(c).Size(glyph, glyph).TextColor(ink).PassThrough()
			} else {
				ui.Icon(c, symbol).Size(glyph, glyph).TextColor(ink).PassThrough()
			}
		})
	})
}

func (a *mobileApp) turnReceipt(c *ui.Context, parent *mygo.Window, sid string, turn domain.Turn, receipt mobileclient.ReplyReceipt, requests []domain.PendingRequest, items []domain.Item,
	historyActions, holding, activeToolFooter, toolsShowProgress, previewTail bool) {
	p := palette(c)
	if holding {
		mobileReplyProgress(c, "正在回复", turn.StartedAt, turn.CompletedAt, true)
		return
	}
	if turn.Status.Active() {
		if !toolsShowProgress {
			status, start, live := a.liveTurnStatus(sid, turn, requests, items)
			mobileReplyProgress(c, status, start, nil, live)
		}
		if activeToolFooter {
			a.replyFooter(c, parent, sid, turn, receipt, false, "", previewTail)
		}
		return
	}
	status := ""
	switch turn.Status {
	case domain.ExecutionCompleted:
		if len(receipt.Reply) == 0 {
			status = "本轮完成"
		}
	case domain.ExecutionInterrupted:
		status = "已停止"
	case domain.ExecutionFailed:
		status = "本轮失败"
	case domain.ExecutionAbandoned:
		status = "已手动结束 · 结果未确认"
	default:
		status = "执行状态待核实"
	}
	a.replyFooter(c, parent, sid, turn, receipt, historyActions, status, previewTail)
	if turn.Status == domain.ExecutionFailed && turn.Error != "" {
		label(c, turn.Error, footerTimeSize, p.secondary).Selectable()
	}
}

const waitingForRequest = "等待处理"

func mobileReplyProgress(c *ui.Context, status string, start, finish *time.Time, live bool) {
	mobileProgressRow(c, status, live, func() {
		if start != nil && !start.IsZero() {
			mobileReplyClock(c, *start, finish)
		}
	})
}

func mobileProgressRow(c *ui.Context, status string, live bool, details func()) {
	p := palette(c)
	font := mobileStatusFont(c, captionTextSize)
	ui.Row(c).MinHeight(transcriptMetadataHeight).AlignItems(ui.Center).Children(func() {
		ui.Row(c.Key("line")).MinWidth(0).Shrink(1).Gap(6).AlignItems(ui.Baseline).Children(func() {
			if live || status == waitingForRequest {
				dot := p.active
				if status == waitingForRequest {
					dot = p.warning
				}
				transcriptStatusDot(c.Key("status-dot"), status, font, dot)
			}
			label(c, status, captionTextSize, p.secondary).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).MinWidth(0).Shrink(1)
			details()
		})
	})
}

func mobileRunningClock(c *ui.Context, start time.Time) {
	mobileReplyClock(c, start, nil)
}

func mobileReplyClock(c *ui.Context, start time.Time, finish *time.Time) {
	mobileClock(c, start, finish, mobileStatusFont(c, captionTextSize), nil)
}

func mobileClock(c *ui.Context, start time.Time, finish *time.Time, font ui.Font, sweep *mobileToolSweep) {
	elapsedAt := func(now time.Time) time.Duration {
		if finish != nil && !finish.IsZero() {
			return max(0, finish.Sub(start))
		}
		return max(0, now.Sub(start))
	}
	font.Features = "tnum"
	value := mobileElapsed(elapsedAt(c.Now()).Milliseconds())
	layout := ui.ShapeText(value, font, 0)
	width, _ := layout.Size()
	baseline := layout.Lines()[0].Baseline
	invalidate := c.Services().Invalidate
	mobileInlineText(c, "本轮计时，开始于 "+start.Local().Format("15:04:05"), font, width, func(p *ui.Painter, r ui.Rect, rowBaseline float32) {
		elapsed := elapsedAt(p.Now())
		if next := mobileElapsed(elapsed.Milliseconds()); next != value {
			value = next
			layout = ui.ShapeText(value, font, 0)
			baseline = layout.Lines()[0].Baseline
			if measured, _ := layout.Size(); math.Abs(float64(measured-width)) > .5 {
				invalidate()
			}
		}
		p.TextLayout(layout, r.X, rowBaseline-baseline, sweep.color(p, r, palette(c).secondary))
		if finish == nil || finish.IsZero() {
			p.After(time.Second - elapsed%time.Second)
		}
	}).Shrink(0)
}

func (a *mobileApp) liveTurnStatus(sid string, turn domain.Turn, requests []domain.PendingRequest, items []domain.Item) (string, *time.Time, bool) {
	if !a.model.Connected || a.model.ConnectionPhase == app.ConnectionSyncing || a.model.ConnectionPhase == app.ConnectionSyncFailed {
		return "执行状态待核对", nil, false
	}
	waiting := func(r domain.PendingRequest) bool {
		return r.SessionID == sid && r.WorkID == "" && (r.TurnID == "" || r.TurnID == turn.ID) && pendingRequest(r)
	}
	if slices.ContainsFunc(requests, waiting) || slices.ContainsFunc(a.model.Snapshot.PendingRequests, waiting) {
		return waitingForRequest, nil, false
	}
	latest := mobileclient.LatestTurnActivity(items, turn.ID)
	start := turn.StartedAt
	for _, item := range slices.Backward(items) {
		if item.TurnID != turn.ID {
			continue
		}
		if start != nil && item.Kind == "user" && (item.Status == "" || item.Status == "sent") && item.SentAt != nil && item.SentAt.After(*start) {
			start = item.SentAt
		}
	}
	status := "进行中"
	if turn.Status == domain.ExecutionStarting {
		status = "正在准备"
	}
	if latest != nil && !latest.Final {
		switch latest.Kind {
		case "assistant":
			status = "正在回复"
		case "tool":
			status = "正在执行工具"
			if latest.Presentation == "command" || latest.Presentation == "" {
				status = "正在执行命令"
			}
		case "reasoning", "activity":
			if latest.Presentation == "reasoning" {
				status = "正在思考"
			}
		}
	}
	return status, start, true
}

func (a *mobileApp) replyFooter(c *ui.Context, parent *mygo.Window, sid string, timing domain.Turn, receipt mobileclient.ReplyReceipt, historyActions bool, status string, previewTail bool) {
	p := palette(c)
	s := a.sessionByID(sid)
	intermediate := receipt.BeforeInput != ""
	if intermediate {
		timing.CompletedAt, timing.DurationMS = receipt.CompletedAt, receipt.DurationMS
	}
	reply := strings.Join(receipt.Reply, "\n\n")
	elapsed := "用时 —"
	if timing.DurationMS != nil {
		elapsed = "用时 " + strings.ReplaceAll(mobileElapsed(*timing.DurationMS), " ", "")
	}
	canFork := historyActions && parent != nil && timing.ID != "" && a.forkAvailable(s)
	fork := footerAction{title: "从这里分叉", icon: forkSymbol, disabled: !canFork, click: func() { a.openFork(parent, s, timing.ID) }}
	if intermediate {
		fork.title = "轮次中途无法分叉"
	}
	details := footerAction{title: "回复详情", icon: infoSymbol, menu: func(menu *ui.Menu) {
		menu.Item("输入 " + usageValue(timing.InputTokens)).Disabled(true)
		menu.Item("输出 " + usageValue(timing.OutputTokens)).Disabled(true)
		menu.Item("缓存命中 " + usageValue(timing.CachedInputTokens)).Disabled(true)
	}}
	if reply == "" && status != "" {
		font := mobileStatusFont(c, footerTimeSize)
		ui.Row(c.Key("turn-outcome")).MinHeight(mobileStatusLineHeight(font)).AlignItems(ui.Baseline).Gap(6).Children(func() {
			if timing.Status == domain.ExecutionCompleted {
				transcriptSymbol(c, checkSymbol, status, font, 12, p.active)
			}
			oneLine(c, status, footerTimeSize, p.secondary).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font))
		})
	}
	copyTitle, copyText := "复制回复", reply
	if reply == "" {
		copyTitle, copyText = "复制工具输出", strings.Join(receipt.ToolOutput, "\n\n")
	}
	copy := copyFooterAction(copyTitle, copyText)
	copy.disabled = copyText == ""
	if reply == "" && timing.Status == domain.ExecutionFailed {
		fork.disabled = true
	}
	messageFooter(c, false).Children(func() {
		if intermediate {
			footerActions(c, previewTail, copy, fork)
		} else {
			footerActions(c, previewTail, copy, fork, details)
		}
		footerClock(c, timing.CompletedAt, "完成时间未知")
		if reply != "" && status != "" {
			footerLabel(c, status)
		}
		footerLabel(c, elapsed)
	})
}

var usageNumbers = message.NewPrinter(language.SimplifiedChinese)

func usageValue(tokens *int64) string {
	if tokens == nil {
		return "—"
	}
	return usageNumbers.Sprintf("%d", *tokens)
}

func footerMetadataHeight(c *ui.Context) float32 {
	_, height := c.MeasureText(0, ui.Span{Text: "用时 00:00", Size: footerTimeSize * c.Preferences().TextScale})
	return max(transcriptMetadataHeight, height)
}

func footerMetadata(c *ui.Context) ui.Element {
	return ui.Row(c).MinHeight(footerMetadataHeight(c)).AlignItems(ui.Center).PassThrough()
}

func footerLabel(c *ui.Context, text string) {
	footerMetadata(c).Children(func() {
		oneLine(c, text, footerTimeSize, palette(c).secondary).PassThrough()
	})
}

func footerClock(c *ui.Context, stamp *time.Time, unknownLabel string) {
	text := "—"
	if stamp != nil {
		text = stamp.Local().Format("15:04")
	}
	footerMetadata(c).Children(func() {
		clock := oneLine(c, text, footerTimeSize, palette(c).secondary).Font("monospace").PassThrough()
		if stamp == nil {
			clock.Label(unknownLabel)
		}
	})
}

func messageFooter(c *ui.Context, trailing bool) ui.Element {
	row := ui.Row(c).AlignItems(ui.Start).Wrap().GapX(footerElementGap).GapY(transcriptItemGap)
	if trailing {
		return row.AlignSelf(ui.End).MaxWidthPercent(100)
	}
	return row.FillWidth()
}

type footerAction struct {
	title    string
	icon     *ui.SVG
	click    func()
	menu     func(*ui.Menu)
	disabled bool
}

func footerActions(c *ui.Context, previewTail bool, actions ...footerAction) {
	// Keep 44-point targets contiguous. Adjust the painted glyphs around
	// their midpoint to share the same spacing as the metadata.
	step := float32(footerIconSize + footerElementGap)
	compression := mobileTapSize(c) - step
	middle := float32(len(actions)-1) / 2
	inset := (mobileTapSize(c)-footerIconSize)/2 + middle*compression
	lineHeight := footerMetadataHeight(c)
	targetHeight := max(mobileTapSize(c), lineHeight)
	if previewTail {
		// The preview is tapped as a card. Its last row needs only the visible footer line.
		targetHeight = lineHeight
	}
	ui.Row(c).Shrink(0).Margin(0, -inset).Children(func() {
		for i, action := range actions {
			key := c.Key(action.title)
			offset := (middle - float32(i)) * compression
			// The painted glyph shares the metadata line inside the full touch target.
			button := mobileButton(key, action.title, action.click).MinHeight(targetHeight).Size(mobileTapSize(c), targetHeight).Shrink(0).
				Padding(0, 0, targetHeight-lineHeight).
				AlignItems(ui.Center).Justify(ui.Center).DimDisabled(false).Disabled(action.disabled).
				Children(func() {
					alignedSymbol(key, action.icon, "00:00", mobileStatusFont(key, footerTimeSize), footerIconSize, palette(key).secondary).
						Margin(0, -offset, 0, offset)
				})
			if action.menu != nil {
				button.PopoverMenuButton(action.menu)
			}
		}
	})
}

func copyFooterAction(title, text string) footerAction {
	return footerAction{title: title, icon: copySymbol, click: func() { mygo.Clipboard.WriteText(text) }}
}

func userMessageFooter(c *ui.Context, text string, stamp *time.Time, edit func(), canEdit, previewTail bool) {
	messageFooter(c, true).Margin(transcriptItemGap, 0, 0).Padding(0, transcriptItemGap, 0, (mobileTapSize(c)-footerIconSize)/2).Justify(ui.End).Children(func() {
		copy := copyFooterAction("复制消息", text)
		if edit == nil {
			footerActions(c, previewTail, copy)
		} else {
			footerActions(c, previewTail, copy, footerAction{title: "编辑消息", icon: pencilSymbol, click: edit, disabled: !canEdit})
		}
		footerClock(c, stamp, "发送时间未知")
	})
}

func mobileElapsed(milliseconds int64) string {
	seconds := max(0, milliseconds/1000)
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%d 分 %02d 秒", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%d 小时 %02d 分", seconds/3600, seconds/60%60)
}

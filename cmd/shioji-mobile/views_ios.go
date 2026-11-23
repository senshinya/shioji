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
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/conversationui"
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
	target := float32(0)
	if a.searching {
		target = 1
		a.inlineSearch.update(c, a, a.home)
	}
	progress := root.AnimateTo("session-search", target, ui.Animation{Duration: 230 * time.Millisecond, Ease: ui.EaseOut})
	// Both panes share the root content area, including the native safe-area insets.
	ui.Column(c.Key("home-panes")).Fill().Children(func() {
		ui.Column(c.Key("session-browser")).Fill().Opacity(1 - progress).
			IgnoreInput(a.searching).AccessHidden(a.searching).Children(func() {
			a.sessionList(c)
			if !a.prominentCompose {
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
										ui.Icon(row, item.icon).Size(20, 20).TextColor(p.ink).PassThrough()
										oneLine(row, item.title, 10, p.ink).FontWeight(500)
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
		if a.searching || progress > 0 {
			ui.Column(c.Key("session-search-pane")).Absolute().Top(mobileHeaderSize(c) + 6).Left(0).Right(0).Bottom(0).
				Opacity(progress).IgnoreInput(!a.searching).AccessHidden(!a.searching).Children(func() {
				a.sessionSearchResults(c)
			})
		}
	})
}

func (a *mobileApp) detailView(c *ui.Context) {
	a.detailQueueMenu.drawQueues(c, a, a.detail)
	p := palette(c)
	if a.wide && a.openingConversation(c, true) {
		return
	}
	findShortcut := a.takeFindShortcut(a.detail)
	if a.wide && a.model.SessionID != "" {
		c.Root().Background(p.canvas)
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
	pane.Grow(weight).Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Ease: ui.EaseOut,
		Position: true, Size: true, Exit: &ui.Motion{Collapse: true, Opacity: 1}})
	if weight > 0 {
		pane.Disabled(!visible).Children(render)
	}
}

func (a *mobileApp) conversation(c *ui.Context, parent *mygo.Window, split bool) {
	c.Root().Background(palette(c).canvas)
	if a.openingConversation(c, split) {
		return
	}
	p := palette(c)
	s := a.session()
	if !split && a.takeFindShortcut(parent) {
		a.openConversationFind(parent, s, false)
	}
	detail := a.model.Details[s.ID]
	initial := a.initialSubmissionDetail(parent, s, &detail)
	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		if split {
			a.conversationHeader(c, s.Title, a.conversationHeaderScope(s),
				func() { a.openDevice(parent) }, func() { a.openActivity(parent, s.ID) })
		}
		a.connectionNotice(c.Key("connection"), parent)
		find := a.conversationFind(c, parent, s, detail, false, func(mods ui.Modifiers, key ui.Key) bool {
			rightVisible := split && a.review == nil && (a.parallelDevice != nil || a.parallel != "")
			return (!rightVisible || !a.parallelFindHandle.FocusWithin(c)) && c.Shortcut(mods, key)
		})
		var draft *mobileDraftEditor
		if (initial.caption == "" || s.ID != "") && find == nil && !s.Archived && !a.workspace(s.WorkspaceID).Archived {
			draft = a.bindDraftEditor(c, a.draftTarget())
		}
		a.conversationBody(c, parent, s, detail, find, draft, false, initial)
	})
}

func (a *mobileApp) conversationHeader(c *ui.Context, title, scope string, device, activity func()) {
	p := palette(c)
	ui.Row(c).Gap(16).AlignItems(ui.Center).MinHeight(mobileHeaderSize(c)).PaddingX(16).Children(func() {
		mobileHeaderButton(c, "切换侧栏", panelSymbol, func() { a.callUIKit("toggleSidebar", nil, nil) })
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Baseline).Gap(8).Children(func() {
			oneLine(c.Key("conversation-title"), title, conversationTitle, p.ink).FontWeight(650).Grow(1).Shrink(1).
				ContentTransition(ui.ContentTransition{Animation: &mobileTitleMotion})
			if scope == "" {
				return
			}
			var row ui.Element
			if device != nil {
				row = ui.ButtonBase(c.Key("conversation-scope")).MinHeight(mobileTapSize(c)).
					Role(ui.RoleButton).Label("打开当前设备详情").Value(scope).OnClick(device)
			} else {
				row = ui.Row(c.Key("conversation-scope"))
			}
			row.MinWidth(0).MaxWidthPercent(48).AlignItems(ui.Center).Justify(ui.End).Gap(4).Shrink(1).Children(func() {
				_, glyph := mobileDeviceIcon(a.splitRoot().savedDevice(a.endpoint.DeviceID).Icon)
				alignedSymbol(c, glyph, scope, mobileStatusFont(c, captionTextSize), 11, p.secondary)
				oneLine(c, scope, captionTextSize, p.secondary).Shrink(1)
			})
		})
		if activity != nil {
			mobileHeaderButton(c, "会话活动", moreSymbol, activity)
		}
	})
}

func (a *mobileApp) conversationBody(c *ui.Context, parent *mygo.Window, session domain.Session, detail domain.SessionDetail,
	find *mobileConversationFind, draft *mobileDraftEditor, right bool, initial firstSubmissionView) {
	ui.Column(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
		dock := ui.Column(c.Key("conversation-dock")).Absolute().Left(0).Right(0).Bottom(0).ZIndex(1).PassThrough()
		latest := a.renderTranscript(c, parent, session, detail, !right, find, nil, draft, dock, initial)
		dock.Children(func() {
			a.historyStatus(c, parent, session)
			if initial.caption != "" && draft == nil {
				if session.ID == "" && a.firstSubmissionVisible(parent) {
					// The submitted input is already in the transcript. Keep the
					// read-only dock separate from the pending home draft.
					editor := ui.Local(dock, "first-submission-editor", func() mobileDraftEditor {
						var editor mobileDraftEditor
						editor.Doc = richtext.NewDocument(richtext.NewText(""))
						return editor
					})
					editor.model = a.model
					editor.target = mobileDraftTarget{workspace: a.model.WorkspaceID}
					a.drawComposer(c.Key("conversation-composer"), parent, session, editor, false, right)
				}
				return
			}
			readingImport := a.importedHistoryFooter(c, parent, session, detail)
			if readingImport || find != nil || draft == nil {
				return
			}
			if !right {
				a.conversationEditor = draft
			} else if a.parallelOwner != nil {
				a.parallelEditor = draft
			}
			a.drawComposer(c.Key("conversation-composer"), parent, session, draft, false, right)
		})
		if latest != nil {
			latest()
		}
	})
}

func (a *mobileApp) renderTranscript(c *ui.Context, parent *mygo.Window, session domain.Session, detail domain.SessionDetail,
	interactive bool, find *mobileConversationFind, preview *archiveTranscript, draft *mobileDraftEditor, dock ui.Element, initial firstSubmissionView) func() {
	p := palette(c)
	sid := session.ID
	viewKey := sid
	if initial.key != "" {
		viewKey = initial.key
	}
	if initial.caption != "" {
		interactive = false
	}
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
		if first := a.newSessionSend; first != nil && viewKey == first.operation {
			state = &first.list
		} else {
			state = a.history[sid]
			if state == nil {
				state = &ui.ListState{FollowEnd: true}
				a.history[sid] = state
			}
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
			return nil
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
	older := detail.History != nil && detail.History.Older
	newer := detail.History != nil && detail.History.Newer
	entryLayer := c.Key("session-entry:" + viewKey)
	var emerging bool
	if a.latestSessionEntry == sid && interactive && preview == nil {
		if a.model.Connected && !hasLatestSessionHistory(detail) {
			ui.Column(entryLayer.Key("loading")).Grow(1).Transition(mobileFeedbackFade(120 * time.Millisecond)).Children(func() {
				if read := a.model.HistoryReads[sid]; read != nil && read.Error != "" {
					mobileSessionLoading(c.Key(sid), "", "无法读取会话历史", func() {
						if a.controller != nil {
							a.controller.Select(sid)
						}
					})
				} else {
					mobileSessionLoading(c.Key(sid), "正在读取会话", "", nil)
				}
			})
			return nil
		}
		emerging = true
		a.latestSessionEntry = ""
	}
	var historyVisual *mobileHistoryVisual
	var historyPose conversationui.HistoryExitFrame
	if preview == nil {
		a.reduceConversationMotion = c.Preferences().ReduceMotion
		detail, historyVisual = a.historyVisualDetail(detail, sid)
		if historyVisual != nil {
			historyPose = conversationui.HistoryExitAt(c.Now(), historyVisual.started, historyVisual.input.Restart, a.reduceConversationMotion)
			if !historyPose.Dispatch {
				c.AnimationFrame()
			}
		}
	}
	projection := a.transcriptProjection(session, detail, compaction, timeline, !newer, interactive || preview != nil,
		!a.preview && preview == nil && historyVisual == nil && !stop.pending() && compaction.action(session) == nil)
	items, transcript, groups := projection.items, projection.rows, projection.groups
	liveCompaction := projection.liveCompaction
	var questionStatuses map[string]string
	if projection.hasQuestions {
		questionStatuses = asyncQuestionStatuses(items, a.model, sid, interactive && preview == nil)
	}
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
	frame := ui.Column(entryLayer.Key("transcript")).Grow(1).Basis(0).MinHeight(0)
	var submission *mobileSubmissionProgress
	if draft != nil && preview == nil && !newer {
		submission = ui.Local(frame, "submission-progress", func() mobileSubmissionProgress { return mobileSubmissionProgress{} })
		submission.update(c, a, draft, items)
	}
	submissionCaption := initial.caption
	if submission != nil && submission.caption != "" {
		submissionCaption = submission.caption
		pending := draft.sendScroll.provisional
		if submissionCaption != "" && pending != nil && pending.ID == "input:"+submission.operation &&
			!slices.ContainsFunc(items, func(item domain.Item) bool { return item.ID == pending.ID }) {
			// The worker's item uses the same key and takes over this row.
			first := len(items)
			items = append(slices.Clip(items), *pending)
			at := len(transcript)
			if pending.TurnID != "" && at > 0 && transcript[at-1].Footer == pending.TurnID {
				at--
			}
			transcript = slices.Insert(slices.Clip(transcript), at, mobileclient.TranscriptRow{First: first, End: first + 1})
		}
	}
	if len(transcript) == 0 && !older && !newer && submissionCaption == "" {
		frame.Children(func() {
			if preview != nil {
				zoom := c.Zoom()
				label(c, "暂无会话记录", controlTextSize, p.secondary).Padding(4, pageInset, transcriptItemGap).Role(ui.RoleStatus).
					DrawOver(func(_ *ui.Painter, r ui.Rect) { preview.fitHeight((r.Y + r.H) * zoom) })
				return
			}
			read := a.model.HistoryReads[sid]
			if read != nil && read.Error != "" {
				var retry func()
				if interactive && a.controller != nil && a.model.Connected {
					retry = func() { a.controller.Select(sid) }
				}
				mobileSessionLoading(c.Key(sid), "", "无法读取会话历史", retry)
			} else if !detail.HasLoadedHistory() && (!a.preview || read != nil && read.Pending) && a.model.Connected {
				mobileSessionLoading(c.Key(sid), "正在读取会话", "", nil)
			}
		})
		return nil
	}
	dockPosition := ui.Local(frame, "dock-position", func() ui.Handle { return ui.Handle{} })
	if dock.Valid() {
		dock.Bind(dockPosition)
	}
	if emerging {
		frame.Transition(mobileFeedbackFade(120 * time.Millisecond))
	}
	scope := replyHistoryScope{session: sid, thread: session.ThreadID, connection: a.model.ConnectionEpoch,
		snapshot: detail.Sequence, activation: a.replyActivation}
	// UIKit keeps a pushed page mounted while another view is in front.
	front := parent == nil || parent != a.chat || c.WindowActive()
	reading := a.model.HistoryReads[sid]
	motionAllowed := find == nil && !session.Archived && !session.ReadOnlyHistory() &&
		a.model.Connected && detail.HasLoadedHistory() && (detail.History == nil || !detail.History.Invalidated && !newer) &&
		(reading == nil || !reading.Pending) && front && initial.caption == "" && !c.Preferences().ReduceMotion
	canAnimate := motionAllowed && state.FollowingEnd()
	canReveal := interactive && canAnimate
	steer := ui.Local(frame, "steer-footer", func() steerFooterHandoff { return steerFooterHandoff{} })
	localHandoff := submission != nil && submission.operation != ""
	transcript = steer.project(c, scope, transcript, items, a.model.UncertainSubmissions, canReveal && !localHandoff)
	turns := projection.turns

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
	if workLive {
		caption := ui.Local(frame, "activity:"+workTurn.ID, func() mobileActivityCaption { return mobileActivityCaption{} })
		workCaption = caption.update(c, workCaption)
	}
	if liveCompaction != "" {
		workCaption = "正在压缩上下文"
	}
	var toolsShowProgress bool
	localSteer := submission != nil && submission.operation != "" && draft.sendScroll.steer && !draft.sendScroll.queued
	if workLive && liveCompaction == "" && workTurn.ID != "" && (submissionCaption == "" || localSteer) {
		for first, group := range groups {
			if group.Live && items[first].TurnID == workTurn.ID {
				toolsShowProgress = true
				break
			}
		}
	}
	if toolsShowProgress && !stop.pending() && len(transcript) > 0 {
		last := transcript[len(transcript)-1]
		if last.Footer == workTurn.ID && last.Receipt.BeforeInput == "" {
			// The tool group already draws the live status. A second, empty
			// receipt would still reserve the transcript row's padding.
			transcript = transcript[:len(transcript)-1]
		}
	}
	// The local receipt can arrive before the Worker has published the turn.
	// Reserve its eventual footer row while the journal catches up.
	progressRow := -1
	if !newer && !toolsShowProgress && liveCompaction == "" && !stop.pending() {
		if last := len(transcript) - 1; last >= 0 && transcript[last].Footer != "" &&
			transcript[last].Footer == workTurn.ID && transcript[last].Receipt.BeforeInput == "" {
			progressRow = last
		}
		if progressRow < 0 && submissionCaption != "" {
			progressRow = len(transcript)
			transcript = append(slices.Clone(transcript), mobileclient.TranscriptRow{Footer: viewKey})
		}
	}
	replyOrder, lastReply := projection.replyOrder, projection.lastReply

	offset, rows := 0, len(transcript)
	if older {
		offset, rows = 1, rows+1
	}
	if newer {
		rows++
	}
	state.EndOverlay = dock
	state.EndOverlayRelease = 0
	state.Reveals = state.Reveals[:0]
	state.FollowAnimation = nil
	if motionAllowed {
		state.FollowAnimation = &mobileSendHandoffAnimation
	}
	sendMotion := initial.motion
	if sendMotion != nil {
		if !front || c.Preferences().ReduceMotion || sendMotion.source.W <= 0 {
			sendMotion.progress = 1
		} else if sendMotion.progress < 1 {
			sendMotion.advance(frame)
		}
	}
	if draft != nil && sendMotion == nil {
		sendMotion = &draft.sendScroll
		if initial := a.newSessionSend; initial != nil {
			if initial.epoch != a.model.ConnectionEpoch || c.Now().Sub(initial.began) > 15*time.Second {
				a.newSessionSend = nil
			} else if session.WorkspaceID == initial.workspace && slices.ContainsFunc(items, func(item domain.Item) bool {
				return item.ID == "input:"+initial.operation
			}) {
				if !initial.previewed {
					initial.from, initial.composerProgress = sendMotion.composerProgress, sendMotion.composerProgress
					*sendMotion = initial.mobileSendScroll
				}
				if a.draftPage == nil || !initial.previewed {
					a.newSessionSend = nil
				}
			}
		}
		draft.queueMotion.observe(c, detail.Queue, items, a.model.ConnectionEpoch, sendMotion, canAnimate)
		sendMotion.reveal(c, frame, state, a.model, items, transcript, offset, motionAllowed)
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
			if index-offset == progressRow || steer.insertion != "" && row.Receipt.BeforeInput == steer.insertion {
				return transcriptProgressKey{}
			}
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
			state.ScrollToRowWithinContent(index+offset, state.Bounds(c).H*.25, mobilePositionMotion(c))
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
	tail.observe(c.Now(), scope, replyOrder, items,
		canReveal && turns[lastReply.TurnID].Status != domain.ExecutionInterrupted, projection)
	tail.revealEvents(c, state, transcript, items, offset)
	if len(state.Reveals) != 0 {
		state.FollowAnimation = nil
	}
	if historyVisual != nil {
		state.FollowAnimation = nil
		for i, row := range transcript {
			index := i + offset
			if historyVisual.rowExits(row, items) && historyPose.Height < 1 {
				state.Reveals = append(state.Reveals, ui.ListReveal{First: index, End: index + 1, Progress: historyPose.Height})
			}
			if row.Footer == "" && historyVisual.input.Restart {
				id := items[row.First].ID
				if id == historyVisual.preview.ID && historyVisual.placed && !historyVisual.bound {
					state.KeepRowWithinContent(index, historyVisual.y)
					historyVisual.bound = true
				}
				if id == "input:"+historyVisual.input.OperationID {
					if historyVisual.placed {
						state.AnchorRow(index, historyVisual.y)
					}
					a.historyRequest(a.model.WorkKey(sid, "")).visual = nil
				}
			}
		}
	}
	slices.SortFunc(state.Reveals, func(a, b ui.ListReveal) int { return a.First - b.First })
	if jump := a.forkMotionTarget; jump.session == sid && jump.thread == session.ThreadID {
		for i, row := range transcript {
			if row.Footer == jump.turn && row.Receipt.BeforeInput == "" {
				state.KeepRow(i+offset, jump.y)
				a.forkMotionTarget = mobileForkPosition{}
				break
			}
		}
	}
	var latest func()
	var startGap float32
	if preview == nil {
		// Leave room below the header in addition to the half-row gap.
		startGap = 4 + transcriptItemGap/2
	}
	endGap := float32(2)
	if draft != nil || initial.key != "" {
		endGap = 8
	}
	frame.Children(func() {
		list := ui.List(c.Key(viewKey), state, rows, func(index int) {
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
			atEnd := !newer && index+1 == len(transcript)
			previewTail := preview != nil && atEnd
			if entry.Footer != "" {
				row := c.Key(state.Key(index + offset))
				turn := turns[entry.Footer]
				receipt := entry.Receipt
				intermediate := receipt.BeforeInput != ""
				hold := !intermediate && turn.Status == domain.ExecutionCompleted && tail.holdReceipt(row, turn.ID)
				settled := index != progressRow && !hold && (intermediate || !turn.Status.Active() && turn.Status != domain.ExecutionInterrupted)
				bottom := float32(transcriptItemGap / 2)
				if previewTail {
					bottom = transcriptItemGap
				} else if receipt.BeforeInput != "" || index+1 < len(transcript) && transcript[index+1].Footer == "" && items[transcript[index+1].First].Kind == "user" {
					bottom = transcriptTurnGap - transcriptItemGap/2
				}
				body := row
				if settled {
					body = row.Key("settled-footer")
				}
				footer := mobileContent(body, contentMaxWidth).Padding(transcriptItemGap/2, pageInset, bottom)
				if settled && canAnimate {
					fade := mobileFeedbackFade(mobileReplyFade)
					if delay := sendMotion.replyFooterDelay(receipt.BeforeInput); delay > 0 {
						// Give the expanding row room to paint its full status line.
						fade.Animation = &ui.Animation{Duration: fade.Duration, Delay: delay, Ease: fade.Ease}
						fade.ExitDelay = -1
					}
					footer.Transition(fade)
				}
				if previewTail {
					preview.fitTail(c, footer)
				}
				if historyVisual != nil && historyVisual.rowExits(entry, items) {
					historyVisual.paint(footer, entry.Footer, historyPose)
				}

				if !intermediate && canReadReply && !hold {
					a.observeReadCompletion(c, footer, state, session, turn, readVisit)
				}
				footer.Children(func() {
					if index == progressRow {
						body := row.Key("running-footer")
						ui.Column(body).FillWidth().Transition(ui.ElementTransition{
							Duration: mobileReplyFade, Ease: ui.EaseOut, Exit: &ui.Motion{Opacity: 0},
						}).DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
							if canReveal && !localHandoff && bounds.H > 0 {
								steer.observeRunning(entry)
							}
						}).Children(func() {
							status, start, live := a.liveTurnStatus(sid, turn, detail.Requests, items)
							if live && workCaption != "" {
								status = workCaption
							}
							if submissionCaption != "" {
								status, live = submissionCaption, submissionCaption == "进行中"
							}
							mobileTranscriptProgress(body, status, start, live)
						})
						return
					}
					if intermediate {
						a.replyFooter(body, parent, sid, turn, receipt, false, "", previewTail, false)
						return
					}
					a.turnReceipt(body, parent, sid, turn, receipt, detail.Requests, items,
						historyActions, hold, toolsShowProgress && turn.ID == workTurn.ID, previewTail, atEnd, workCaption)
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
			var historyRow *ui.Handle
			if historyActions && item.Kind == "user" {
				historyRow = ui.Local(content, "history-row", func() ui.Handle { return ui.Handle{} })
				content.Bind(historyRow)
			}
			if historyVisual != nil {
				if historyVisual.rowExits(entry, items) {
					historyVisual.paint(content, item.ID, historyPose)
				} else if historyVisual.bound && item.ID == historyVisual.preview.ID && historyPose.Height < 1 {
					content.DrawOver(func(_ *ui.Painter, r ui.Rect) { historyVisual.y = r.Y - state.Bounds(c).Y })
				}
			}
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
					animateCalls := canAnimate && (tail.continuous || !interactive && preview == nil) && tail.arrivals[group.Key].IsZero()
					a.toolGroup(row, parent, session, items[first:end], group, timeline, state, index+offset, workCaption, workStart, toolsShowProgress, animateCalls, target, reveal, decorate)
					return
				}
				var mark *markdown.RangeDecoration
				if decorate != nil {
					mark = decorate(item)
				}
				switch item.Kind {
				case "user", "command":
					editing := item.Kind == "user" && historyActions && preview == nil && a.model.HistoryEdits[sid].ItemID == item.ID
					restarting := historyVisual != nil && historyVisual.input.Restart && item.ID == historyVisual.input.ItemID
					if editing && !restarting {
						fit := ui.Local(content, "history-editor-fit", func() [2]float32 { return [2]float32{} })
						content.DrawOver(func(_ *ui.Painter, r ui.Rect) {
							viewport, dockBounds := state.Bounds(c), dockPosition.Bounds(c)
							if viewport.H > 0 && dockBounds.H > 0 && *fit != [2]float32{r.H, dockBounds.Y} {
								*fit = [2]float32{r.H, dockBounds.Y}
								if overflow := r.Y + r.H - dockBounds.Y; overflow > 0 {
									state.ScrollToRowWithinContent(index+offset, max(8, r.Y-viewport.Y-overflow-8), mobilePositionMotion(c))
									c.Services().Invalidate()
								}
							}
						})
					}
					render := func(row *ui.Context) {
						if editing && !restarting && a.historyEditor(row, parent, session, item) {
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
							a.transcriptUserBubble(row, parent, session, item, runs, mark, sendMotion)
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
									req := a.historyRequest(a.model.WorkKey(sid, ""))
									bounds := historyRow.Bounds(c)
									req.anchorY, req.hasAnchor = bounds.Y-state.Bounds(c).Y, bounds.H > 0
									if req.hasAnchor {
										state.KeepRowWithinContent(index+offset, req.anchorY)
									}
									a.save()
								}
							}
						}
						userMessageFooter(row, message, item.SentAt, edit, canEdit && !restarting, previewTail, sendMotion.actionsOpacity(item.ID))
						if restarting && historyPose.Shatter == 0 && c.Now().Sub(historyVisual.started) >= conversationui.HistoryShatterDuration {
							mobileReplyProgress(row.Key("history-running"), "进行中", nil, nil, true)
						}
					}
					if preview != nil || item.Kind != "user" {
						render(row)
					} else {
						mobileHistoryMessageHeight(row, editing, restarting, render)
					}
				case "assistant":
					turn, hasTurn := turns[item.TurnID]
					if len(item.Questions) != 0 {
						a.asyncQuestionMessage(row, parent, session, item, questionStatuses, interactive && preview == nil)
					} else if item.Text != "" {
						var embedDelta float32
						a.drawTranscriptMarkdown(row, parent, session, float32(a.model.Preferences.ReplySize), mark, func(body *ui.Context, options *markdown.Options) {
							tail.draw(body, item, options, state, &embedDelta)
						}, &embedDelta)
					}
					a.contentParts(row, parent, session, item.Parts)
					if item.Final && !hasTurn && (!session.Status.Active() || item.ID != lastReply.ID) {
						// A reply from old history can have a message but no turn
						// metadata. Its actions and unknown fields are still useful.
						turn.ID = item.TurnID
						footer := row.Key("reply-footer")
						ui.Column(footer).FillWidth().Margin(transcriptItemGap, 0, 0).Children(func() {
							if tail.holdReceipt(footer, turn.ID) {
								mobileReplyProgress(footer, "正在回复", nil, nil, true)
							} else {
								receipt := mobileclient.ReplyReceipt{Reply: []string{item.Text}}
								a.replyFooter(footer, parent, sid, turn, receipt, historyActions, "", previewTail, atEnd)
							}
						})
					}
				case "request":
					a.requestSource(row, parent, sid, item, detail.Requests, historyActions, tail.receiptProgress(row, item.ID), nil)
				case "request_result":
					a.requestResult(row, parent, session, item, tail.receiptProgress(c, item.ID))
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
		}).Grow(1).Basis(0).MinHeight(0).Padding(startGap, 0, endGap)
		a.prefetchTranscript(c, list, state, prefetch, parent, detail, current, parent != nil && preview == nil && historyActions && find == nil && !reveal)
		if interactive && find == nil && (newer || !state.AtEnd() && !state.FollowingEnd()) && a.model.HistoryEdits[sid].ItemID == "" {
			if !workLive {
				workCaption = "最新消息"
				if workTurn.ID != "" && !a.model.Connected {
					workCaption = "状态待确认"
				}
			}
			latest = func() {
				a.transcriptLatest(c.Key("return-latest"), dock, sid, detail, state, prefetch, newer, workCaption, workStart, workLive)
			}
		}
	})
	return latest
}

func (a *mobileApp) drawComposer(c *ui.Context, parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, home, right bool) {
	m := a.model
	p := palette(c)
	targetRef := editor.target
	key, sid := targetRef.key, targetRef.session
	waitingForSession := !home && sid == ""
	draftText := editor.VisibleText(m, key)
	sending, _, uncertain := mobileclient.SubmissionPresentation(m, targetRef.workspace, sid)
	firstInput, firstPending := m.Pending[a.promotedDraftOperation]
	firstPending = firstPending && sid != "" && sid == a.promotedDraftSession
	firstUncertain := firstPending && m.UncertainSubmissions[firstInput.OperationID]
	if firstUncertain {
		uncertain = append(uncertain, firstInput)
	}
	ready := editor.readiness()
	runtimeBlocked := ready.State != domain.RuntimeReady
	var config domain.RunConfig
	if home || waitingForSession {
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
		return !waitingForSession && connected && readyPhase && !session.ContextChanged && !a.draftSaveFailed() && attachmentsReady && !firstPending && !m.HasPendingInputForSession(sid) && !m.PermissionChangePending(sid) && queueEdit.ID == "" && !editor.pending() && !editor.attachmentMenuOpen && m.HasDraftContent(key)
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
	width, inset, topInset, bottomInset := float32(conversationComposerWidth), float32(conversationComposerInset), float32(4), float32(0)
	_, _, covered, _ := c.SafeArea()
	visibleCover := covered * c.Zoom()
	keyboard := mobileclient.ComposerKeyboardVisible(visibleCover)
	root := a.splitRoot()
	dismissed := !home && !keyboard && editor.keyboardVisible && root.composerKeyboardDismissed == parent.NativeHandle()
	if keyboard && editor.focused {
		editor.keyboardVisible = true
	}
	if dismissed {
		root.composerKeyboardDismissed = 0
		editor.keyboardVisible = false
	}
	motion := root.keyboardMotion
	moving := !home && motion.Moving
	wasTracking := editor.trackingKeyboard
	editor.trackingKeyboard = moving && (editor.focused || editor.keyboardVisible || dismissed || wasTracking)
	if home {
		width, inset, topInset, bottomInset = draftComposerWidth, 12, 2, composerBottomGap
	} else {
		// The keyboard extends past the bottom system bar. Keep this gap in
		// the dock so the last message follows the same geometry.
		bottomInset = composerBottomGap / c.Zoom() * min(1, max(0, (visibleCover-40)/60))
	}
	// Let scrollable panels give up height before the fixed input surface.
	mobileContent(c, width).MinHeight(0).PassThrough().Padding(topInset, inset, bottomInset).Gap(composerPanelGap).Children(func() {
		if !home && !waitingForSession {
			a.requestPanels(c, parent, sid, "", false)
			a.recoveryControls(c, parent, session, editor)
			if session.ContextChanged {
				a.branchContextNotice(c.Key("branch-context"), parent, session, editor, right, connected)
			}
			a.goalControl(c.Key("session-goal"), parent, session)
			referencePanelOpen := editor.query.Marker != 0 && editor.query != editor.dismissed
			a.queue(c, parent, session, editor, queue, !isTablet() && keyboard && referencePanelOpen)
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
			mobileProgressRow(c, "已停止", nil, false, nil)
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
		if !home {
			conversationui.UsageLimitNotice(c.Key("usage-limit:"+key), m.UsageLimitForSession(sid, c.Now()), p.panel, p.ink, p.secondary, c.Preferences().TextScale, 52, 0)
		}
		a.pathAttachmentPanel(c, parent, editor)
		a.draftReferences(c, parent, editor)
		editor.referencePanel(a, c, parent)
		needsSpace := editor.Doc.Text().Len() != 0 || len(editor.VisibleAttachments(m, key)) != 0 || queueEdit.ID != "" || modelUnavailable
		expanded := needsSpace || editor.focused && (home || keyboard)
		usingKeyboardMotion := editor.trackingKeyboard && motion.Morphing && motion.Peak > motion.Bottom
		if !usingKeyboardMotion || needsSpace {
			editor.keyboardTransition = mobileclient.ComposerKeyboardTransition{}
		}
		var keyboardProgress *float32
		if !home && !needsSpace && (editor.trackingKeyboard || wasTracking || dismissed || keyboard && editor.focused) {
			progress := float32(0)
			switch {
			case usingKeyboardMotion:
				progress = editor.keyboardTransition.Progress(visibleCover, motion.Bottom, motion.Peak)
			case expanded:
				progress = 1
			}
			keyboardProgress = &progress
		}
		send := &editor.sendScroll
		releasing := send.operation != "" && send.steer && !send.queued && send.started
		surface, geometry := mobileComposerSurface(c, key, home, expanded, send, keyboardProgress, queueEdit.ID != "")
		if !home {
			growth := mobileComposerExtraHeight(c, surface, editor, draftText, geometry.EditorHeight,
				geometry.ControlsProgress, queueEdit.ID != "")
			geometry.Height += growth
			geometry.EditorHeight += growth
			geometry.ControlsY += growth
			surface.Height(geometry.Height)
			if list := a.history[sid]; releasing && list != nil {
				// Aim the inserted message past the parts of the dock closing with it.
				if !expanded {
					settled := mobileclient.ComposerGeometry(0, false, composerLineHeight(c), mobileTapSize(c))
					list.EndOverlayRelease += max(0, geometry.Height-settled.Height)
				}
			}
		}
		surface.DrawOver(func(_ *ui.Painter, r ui.Rect) { editor.sendScroll.composerBounds = r })
		if editor.sendScroll.workspace != "" && editor.sendScroll.started && editor.sendScroll.composerSource.W > 0 && !c.Preferences().ReduceMotion {
			surface.Transition(ui.ElementTransition{From: &editor.sendScroll.composerSource, Duration: 400 * time.Millisecond,
				Ease: mobileComposerMotionEase, Position: true, Size: true})
		}
		if dismissed && editor.focused {
			surface.Focus(ui.FocusOptions{PreventScroll: true}).FocusRing(false)
		}
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
			if send.flying("input:" + send.operation) {
				placeholder = ""
			}
			ui.Column(c).Margin(geometry.TextTop, geometry.TextRight, 0, geometry.TextInset).
				Padding(mobileclient.ComposerEditorPadding, 4).Height(geometry.EditorHeight).Children(func() {
				title := "消息"
				if right {
					title = "右侧会话草稿"
				}
				editor.field(a, c, parent, title, placeholder, waitingForSession, 0, func(steer bool) {
					if queueEdit.ID != "" && !steer {
						saveQueue()
					} else {
						submit(steer)
					}
				})
			})
			ui.Row(c).Absolute().Left(mobileclient.ComposerControlInset).Right(mobileclient.ComposerControlInset).
				Top(geometry.ControlsY).Height(mobileTapSize(c)).AlignItems(ui.Center).Gap(mobileclient.ComposerControlGap).PassThrough().Children(func() {
				attachmentSize := float32(17)
				if home {
					attachmentSize = 12
				}
				attachmentTitle := "添加附件"
				if right {
					attachmentTitle = "添加右侧附件"
				}
				a.composerAttachmentMenu(c, parent, editor, attachmentTitle, attachmentSize).Disabled(waitingForSession).DimDisabled(false)
				options := c.Key("run-controls")
				row := ui.Row(options).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(mobileclient.ComposerControlGap).
					Opacity(geometry.ControlsOpacity).PassThrough()
				ui.Inert(row, waitingForSession || geometry.ControlsOpacity < 1)
				row.Children(func() {
					a.runControls(options, parent, editor, ready, config, modelUnavailable, home, right)
				})
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
				case (sending || firstPending && !firstUncertain) && connected:
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
					sendButton(c, title, sendSymbol, home, !canSubmit() && !canSteer(), nil).Menu(func(menu *ui.Menu) {
						if menu.Item("加入待发队列").Disabled(!canSubmit()).Chosen() {
							submit(false)
						}
						if menu.Item("介入当前轮").Disabled(!canSteer()).Chosen() {
							submit(true)
						}
					})
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
	historyActions, holding, toolsShowProgress, previewTail, atEnd bool, caption string) {
	p := palette(c)
	if holding {
		mobileReplyProgress(c, "正在回复", turn.StartedAt, turn.CompletedAt, true)
		return
	}
	if turn.Status.Active() {
		if !toolsShowProgress {
			status, start, live := a.liveTurnStatus(sid, turn, requests, items)
			if live && caption != "" {
				status = caption
			}
			mobileReplyProgress(c, status, start, nil, live)
		}
		return
	}
	status := ""
	switch turn.Status {
	case domain.ExecutionCompleted:
	case domain.ExecutionInterrupted:
		status = "已停止"
	case domain.ExecutionFailed:
		status = "本轮失败"
		if turn.UsageLimit != nil {
			status = "用量耗尽"
		}
	case domain.ExecutionAbandoned:
		status = "已结束 · 结果未确认"
	default:
		status = "执行状态待核实"
	}
	if turn.Status == domain.ExecutionFailed && len(receipt.Reply) == 0 {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Baseline).Children(func() {
			footerLabel(c, status)
			if turn.Error != "" && turn.UsageLimit == nil {
				label(c, turn.Error, captionTextSize, p.secondary).MinWidth(0).Shrink(1).Selectable()
			}
			if turn.DurationMS != nil {
				footerLabel(c, mobileElapsed(*turn.DurationMS))
			}
		})
		return
	}
	a.replyFooter(c, parent, sid, turn, receipt, historyActions, status, previewTail, atEnd)
	if turn.Status == domain.ExecutionFailed && turn.Error != "" && turn.UsageLimit == nil {
		label(c, turn.Error, captionTextSize, p.secondary).Selectable()
	}
}

const waitingForRequest = "等待处理"

func mobileReplyProgress(c *ui.Context, status string, start, finish *time.Time, live bool) {
	mobileProgressRow(c, status, start, live, func(sweep *mobileToolSweep) {
		if start != nil && !start.IsZero() {
			clock := c.Key("elapsed")
			slot := ui.Row(clock).Shrink(0).Transition(mobileFeedbackFade(120 * time.Millisecond))
			slot.Children(func() { mobileClock(clock, *start, finish, mobileStatusFont(c, captionTextSize), sweep) })
		}
	})
}

func mobileProgressRow(c *ui.Context, status string, started *time.Time, live bool, details func(*mobileToolSweep)) {
	p := palette(c)
	font := mobileStatusFont(c, captionTextSize)
	ui.Row(c).MinHeight(transcriptMetadataHeight).AlignItems(ui.Center).Children(func() {
		line := c.Key("line")
		row := ui.Row(line).MinWidth(0).Shrink(1).Gap(6).AlignItems(ui.Baseline)
		var sweep *mobileToolSweep
		if live {
			phase := *ui.Local(row, "progress-sweep-start", c.Now)
			if started != nil && !started.IsZero() {
				phase = *started
			}
			sweep = newMobileToolSweep(line, row, phase)
		}
		row.Children(func() {
			if live {
				mobileToolStatusDot(line.Key("status-dot"), status, font, sweep)
			} else if status == waitingForRequest {
				transcriptStatusDot(line.Key("status-dot"), status, font, p.warning, false)
			}
			sweep.caption(line.Key("status"), status, ui.ContentTransition{Animation: &ui.Animation{Duration: mobileReplyFade}})
			if details != nil {
				details(sweep)
			}
		})
	})
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
	glyphs := ui.Shape(value, font)
	invalidate := c.Services().Invalidate
	mobileInlineText(c, "本轮计时，开始于 "+start.Local().Format("15:04:05"), font, width, func(p *ui.Painter, r ui.Rect, rowBaseline float32) {
		elapsed := elapsedAt(p.Now())
		if next := mobileElapsed(elapsed.Milliseconds()); next != value {
			value = next
			layout = ui.ShapeText(value, font, 0)
			glyphs = ui.Shape(value, font)
			if measured, _ := layout.Size(); math.Abs(float64(measured-width)) > .5 {
				invalidate()
			}
		}
		sweep.text(p, glyphs, r.X, rowBaseline, palette(c).secondary)
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

func (a *mobileApp) replyFooter(c *ui.Context, parent *mygo.Window, sid string, timing domain.Turn, receipt mobileclient.ReplyReceipt, historyActions bool, status string, previewTail, atEnd bool) {
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
	anchor := ui.Local(ui.Spacer(c.Key("fork-anchor")).Absolute().Size(0, 0), "rect", func() ui.Rect { return ui.Rect{} })
	fork := footerAction{title: "从这里分叉", icon: forkSymbol, disabled: !canFork, click: func() {
		if list := a.history[sid]; list != nil && anchor.H > 0 {
			req := a.historyRequest(a.model.WorkKey(sid, ""))
			req.anchorY, req.hasAnchor = anchor.Y-list.Bounds(c).Y, true
		}
		a.openFork(parent, s, timing.ID)
	}}
	if intermediate {
		fork.title = "轮次中途无法分叉"
	}
	copyTitle, copyText := "复制回复", reply
	if reply == "" {
		copyTitle, copyText = "复制工具输出", strings.Join(receipt.ToolOutput, "\n\n")
	}
	copy := copyFooterAction(copyTitle, copyText)
	copy.disabled = copyText == ""
	messageFooter(c, false).DrawOver(func(_ *ui.Painter, r ui.Rect) { *anchor = r }).Children(func() {
		footerActions(c, previewTail, atEnd, copy, fork)
		footerClock(c, timing.CompletedAt, "完成时间未知")
		if status != "" {
			footerLabel(c, status)
		}
		footerLabel(c, elapsed)
	})
}

func footerMetadataHeight(c *ui.Context) float32 {
	_, height := c.MeasureText(0, ui.Span{Text: "用时 00:00", Size: captionTextSize * c.Preferences().TextScale})
	return max(transcriptMetadataHeight, height)
}

func footerMetadata(c *ui.Context) ui.Element {
	return ui.Row(c).MinHeight(footerMetadataHeight(c)).AlignItems(ui.Center).PassThrough()
}

func footerLabel(c *ui.Context, text string) {
	footerMetadata(c).Children(func() {
		oneLine(c, text, captionTextSize, palette(c).secondary).PassThrough().
			ContentTransition(ui.ContentTransition{Numeric: true, Animation: &mobileTitleMotion})
	})
}

func footerClock(c *ui.Context, stamp *time.Time, unknownLabel string) {
	text := "—"
	if stamp != nil {
		text = stamp.Local().Format("15:04")
	}
	footerMetadata(c).Children(func() {
		clock := oneLine(c, text, captionTextSize, palette(c).secondary).Font("monospace").PassThrough().ContentTransition(ui.ContentTransition{Numeric: true, Animation: &mobileTitleMotion})
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
	copy     string
	click    func()
	disabled bool
}

func footerActions(c *ui.Context, previewTail, atEnd bool, actions ...footerAction) {
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
	padding := targetHeight - lineHeight
	touchOverflow := float32(0)
	if atEnd {
		// Keep the final line against the reply and let the touch area extend
		// evenly above and below it without adding transcript height.
		touchOverflow = padding / 2
	}
	ui.Row(c).Shrink(0).Margin(-touchOverflow, -inset).Children(func() {
		for i, action := range actions {
			key := c.Key(action.title)
			offset := (middle - float32(i)) * compression
			// The painted glyph shares the metadata line inside the full touch target.
			activate := action.click
			var copied *time.Time
			button := mobileButton(key, action.title, nil).MinHeight(targetHeight).Size(mobileTapSize(c), targetHeight).Shrink(0).
				AlignItems(ui.Center).Justify(ui.Center).DimDisabled(false).Disabled(action.disabled)
			if action.copy != "" {
				copied = ui.Local(button, "copy-feedback", func() time.Time { return time.Time{} })
				activate = func() {
					action.click()
					*copied = time.Now().Add(2 * time.Second)
				}
			}
			if activate != nil {
				button.OnClick(activate)
			}
			button.Children(func() {
				glyph, copiedNow := action.icon, copied != nil && c.Now().Before(*copied)
				if copiedNow {
					glyph = checkSymbol
					c.After(copied.Sub(c.Now()))
				}
				alignedSymbol(key.Key(copiedNow), glyph, "00:00", mobileStatusFont(key, captionTextSize), footerIconSize, palette(key).secondary).
					Margin(0, -offset, 0, offset).Transition(mobileFeedbackFade(120 * time.Millisecond))
			})
			button.Padding(touchOverflow, 0, padding-touchOverflow)
		}
	})
}

func copyFooterAction(title, text string) footerAction {
	return footerAction{title: title, icon: copySymbol, copy: text, click: func() { mygo.Clipboard.WriteText(text) }}
}

func userMessageFooter(c *ui.Context, text string, stamp *time.Time, edit func(), canEdit, previewTail bool, opacity float32) {
	footer := messageFooter(c, true).Margin(transcriptItemGap, 0, 0).Padding(0, transcriptItemGap, 0, (mobileTapSize(c)-footerIconSize)/2).Justify(ui.End)
	if opacity < 1 {
		footer.Opacity(opacity)
		ui.Inert(footer, true)
	}
	footer.Children(func() {
		copy := copyFooterAction("复制消息", text)
		if edit == nil {
			footerActions(c, previewTail, false, copy)
		} else {
			footerActions(c, previewTail, false, copy, footerAction{title: "编辑消息", icon: pencilSymbol, click: edit, disabled: !canEdit})
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

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileGoalOperation struct {
	request domain.GoalRequest
	thread  string
	message string
	busy    bool
}

func goalStatus(state domain.GoalState) string {
	switch state {
	case domain.GoalActive:
		return "执行中"
	case domain.GoalPaused:
		return "已暂停"
	case domain.GoalBlocked:
		return "已阻塞"
	case domain.GoalUsageLimited:
		return "用量受限"
	case domain.GoalBudgetLimited:
		return "已达预算"
	case domain.GoalComplete:
		return "已完成"
	default:
		return "状态未知"
	}
}

func goalElapsed(seconds int64) string {
	if seconds >= 3600 {
		return fmt.Sprintf("%d 小时 %d 分", seconds/3600, seconds%3600/60)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%d 秒", max(seconds, 0))
}

func goalContinuation(c *ui.Context, name string) {
	if name == "" {
		name = "目标自动续跑"
	}
	p := palette(c)
	font := mobileStatusFont(c, captionTextSize)
	ui.Row(c).AlignItems(ui.Baseline).MinHeight(24).Gap(6).Children(func() {
		transcriptSymbol(c, goalSymbol, name, font, 14, p.secondary)
		label(c, name, captionTextSize, p.secondary).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).Selectable()
	})
}

func (a *mobileApp) canControlGoal(parent *mygo.Window, s domain.Session) bool {
	m := a.model
	return s.ThreadID != "" && a.visibleLinkSource(parent, s) && !s.Archived && !s.ReadOnlyHistory() && !a.workspace(s.WorkspaceID).Archived && m.Connected &&
		m.WorkerMinor >= protocol.GoalMinor && m.SessionReadiness(s).Capabilities.Goals && (a.controller != nil || a.preview)
}

func (a *mobileApp) canResumeGoal(s domain.Session) bool {
	if !s.Goal.CanResume() || !s.Status.CanSubmit() || s.ContextChanged || s.ExecutionGuard != "" || s.Config.Plan || a.model.HasPendingInputForSession(s.ID) {
		return false
	}
	for _, request := range a.model.Details[s.ID].Requests {
		if request.State.AwaitingResolution() {
			return false
		}
	}
	return true
}

type mobileGoalPresence struct {
	goal   domain.Goal
	height float32
}

func (a *mobileApp) goalControl(c *ui.Context, parent *mygo.Window, s domain.Session) {
	if s.ID == "" {
		return
	}
	key := a.model.WorkKey(s.ID, "")
	operation := a.goalOperations[key]
	if operation != nil && operation.thread != s.ThreadID {
		delete(a.goalOperations, key)
		operation = nil
	}
	scope := c.Key(key).Key(s.ThreadID).Key(a.model.ConnectionEpoch)
	frame := ui.Column(scope).FillWidth().Shrink(0).Clip()
	presenceState := ui.Local(frame, "goal-presence", func() mobileGoalPresence { return mobileGoalPresence{} })
	if a.goalEntranceSession == key {
		// The worker may accept the first goal before its conversation mounts.
		frame.Jump("presence", 0)
		a.goalEntranceSession = app.WorkKey{}
	}
	goal := s.Goal
	active := goal != nil && strings.TrimSpace(goal.Objective) != ""
	showReceipt := operation != nil && operation.message != ""
	target := float32(0)
	if active {
		presenceState.goal = *goal
		target = 1
	} else if showReceipt {
		*presenceState = mobileGoalPresence{}
		target = 1
	}
	presence := target
	if c.Preferences().ReduceMotion || !active && presenceState.goal.Objective == "" {
		frame.Jump("presence", target)
	} else {
		presence = frame.AnimateTo("presence", target, ui.Animation{Duration: 180 * time.Millisecond})
	}
	if presence == 0 {
		if !active {
			*presenceState = mobileGoalPresence{}
		}
		frame.Hidden(true)
		return
	}
	if !active && !showReceipt {
		goal = &presenceState.goal
	}
	height := presenceState.height
	if height == 0 {
		height = mobileTapSize(c)
	}
	if presence < 1 {
		frame.Height(height*presence).Margin(0, 0, -composerPanelGap*(1-presence))
	}
	p := palette(c)
	sectionKey := scope.Key("goal-content")
	if goal != nil {
		sectionKey = sectionKey.Key(goal.CreatedAt)
	}
	frame.Children(func() {
		section := ui.Column(sectionKey).FillWidth()
		if presence < 1 {
			section.Absolute().Left(0).Right(0).Top(0).Opacity(presence).
				Transform(ui.Translation(0, -height*(1-presence)/2))
		}
		if active {
			section.DrawOver(func(_ *ui.Painter, bounds ui.Rect) { presenceState.height = bounds.H })
		} else if !showReceipt {
			section.Disabled(true).DimDisabled(false)
		}
		expanded := ui.Local(section, "objective-expanded", func() bool { return false })
		section.Children(func() {
			if goal != nil && strings.TrimSpace(goal.Objective) != "" {
				scope := sectionKey
				canControl := a.canControlGoal(parent, s)
				pending := operation != nil && operation.request.OperationID != ""
				ui.Row(scope).FillWidth().MinHeight(mobileTapSize(c)).PaddingX(4).AlignItems(ui.Center).Gap(4).Children(func() {
					status := goalStatus(goal.State)
					chip := mobileButton(scope.Key("summary"), "目标摘要", func() { *expanded = !*expanded }).KeepFocus().
						Value(status + "，" + goal.Objective).MinHeight(mobileTapSize(c)).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(6)
					chip.Children(func() {
						font := ui.Font{Size: captionTextSize * c.Preferences().TextScale}
						alignedSymbol(scope, goalSymbol, status, font, 15, p.secondary).Shrink(0).PassThrough()
						oneLine(scope, status, captionTextSize, p.secondary).Shrink(0).PassThrough()
						oneLine(scope, goal.Objective, controlTextSize, p.ink).Grow(1).MinWidth(0).Ellipsis("…").PassThrough()
					})
					oneLine(scope.Key("elapsed"), goalElapsed(goal.ElapsedSeconds), captionTextSize, p.secondary).Shrink(0)
					if canControl && (goal.Active() || goal.CanResume()) {
						name, control := "暂停", domain.PauseGoal
						if !goal.Active() {
							name, control = "恢复", domain.ResumeGoal
						}
						action(scope.Key(control), name, func() { a.controlGoal(parent, s, control) }).Label(name + "目标").KeepFocus().
							Disabled(pending || control == domain.ResumeGoal && !a.canResumeGoal(s))
					}
					if canControl {
						iconButton(scope.Key("more"), "目标操作", moreSymbol, 15, nil).KeepFocus().Disabled(pending).Menu(func(menu *ui.Menu) {
							if menu.Item("清除目标").Danger().Chosen() {
								a.confirmClearGoal(parent, s)
							}
						})
					}
				})
				mobileDisclosureBody(scope.Key("expanded"), *expanded, func(body *ui.Context) {
					label(body.Key("objective"), goal.Objective, controlTextSize, p.ink).Padding(0, 8, 8, 25).Selectable()
				})
			}
			if operation != nil && operation.message != "" {
				ui.Row(c.Key("goal-receipt")).AlignItems(ui.Center).PaddingX(4).Gap(6).Children(func() {
					label(c, operation.message, captionTextSize, p.warning).Grow(1).MinWidth(0)
					if operation.request.OperationID != "" {
						action(c, "核对目标操作", func() { a.sendGoal(parent, key, operation, true) }).KeepFocus().Disabled(operation.busy || !a.model.Connected)
					} else {
						iconButton(c, "关闭目标提示", closeSymbol, 14, func() { delete(a.goalOperations, key) })
					}
				})
			}
		})
	})
}

func (a *mobileApp) confirmClearGoal(parent *mygo.Window, s domain.Session) {
	model := a.model
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: parent, Type: mygo.MessageWarning,
			Message: "清除目标？", Detail: "清除后，此目标将不再自动续跑。", Buttons: []string{"取消", "清除目标"}, DestructiveButtons: []int{1}})
		if err == nil && result.Button == 1 {
			mygo.RunOnMain(func() {
				if a.model == model {
					a.controlGoal(parent, s, domain.ClearGoal)
				}
			})
		}
	}()
}

func (a *mobileApp) controlGoal(parent *mygo.Window, s domain.Session, action domain.GoalAction) {
	current := a.sessionByID(s.ID)
	if s.ThreadID != current.ThreadID || !a.canControlGoal(parent, current) || s.Goal == nil || !s.Goal.SameTarget(current.Goal) ||
		action == domain.PauseGoal && !current.Goal.Active() || action == domain.ResumeGoal && !a.canResumeGoal(current) {
		return
	}
	key := a.model.WorkKey(s.ID, "")
	if prior := a.goalOperations[key]; prior != nil && prior.thread == s.ThreadID && prior.request.OperationID != "" {
		return
	}
	request := domain.GoalRequest{OperationID: domain.NewID(), SessionID: s.ID, Target: *current.Goal, Action: action}
	state := &mobileGoalOperation{request: request, thread: s.ThreadID}
	if a.goalOperations == nil {
		a.goalOperations = make(map[app.WorkKey]*mobileGoalOperation)
	}
	a.goalOperations[key] = state
	a.sendGoal(parent, key, state, false)
}

func (a *mobileApp) sendGoal(parent *mygo.Window, key app.WorkKey, state *mobileGoalOperation, query bool) {
	m := a.model
	request := state.request
	current := a.sessionByID(key.SessionID)
	if state.busy || !m.Connected || key.DeviceID != m.Snapshot.DeviceID || current.ThreadID != state.thread || !a.visibleLinkSource(parent, current) ||
		a.goalOperations[key] != state || request.OperationID == "" {
		return
	}
	method, input := "session.goal.control", any(request)
	if query {
		method, input = "operation", map[string]string{"id": request.OperationID}
	}
	state.busy = true
	done := func(raw json.RawMessage, err error) {
		if a.goalOperations[key] != state {
			return
		}
		state.busy = false
		if err != nil {
			state.message = err.Error()
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (remote.Code == "notSent" || remote.Code == "unsupportedCapability" || query && remote.Code == "notFound") {
				state.request = domain.GoalRequest{}
			}
		} else {
			var receipt domain.Operation
			if json.Unmarshal(raw, &receipt) != nil || receipt.ID != request.OperationID || receipt.SessionID != request.SessionID || receipt.Kind != "goal" {
				state.message = "目标操作回执不一致"
			} else {
				switch receipt.State {
				case domain.OperationConfirmed:
					delete(a.goalOperations, key)
				case domain.OperationRejected, domain.OperationAbandoned:
					state.request = domain.GoalRequest{}
					state.message = receipt.Error
					if state.message == "" {
						state.message = "目标操作未完成"
					}
				default:
					state.message = "目标操作尚未确认"
				}
			}
		}
		if a.model == m {
			a.refresh()
		}
	}
	if a.preview {
		a.previewControlGoal(request, query, done)
	} else if a.controller == nil || !a.controller.Do(app.Job{Method: method, Input: input, ConnectionEpoch: m.ConnectionEpoch,
		Apply: func(_ *app.Model, raw json.RawMessage, err error) { done(raw, err) },
	}) {
		state.busy, state.message = false, "设备繁忙，请稍后重试"
		if !query {
			state.request = domain.GoalRequest{}
		}
	}
	a.refresh()
}

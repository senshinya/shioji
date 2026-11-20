package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func goalTagRange(text string) (start, end int, ok bool) {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	if _, goal := domain.GoalCommand(trimmed); !goal || len(trimmed) == len(slashGoal) {
		return 0, 0, false
	}
	start = utf8.RuneCountInString(text[:len(text)-len(trimmed)])
	return start, start + len(slashGoal), true
}

func goalTagAttributes(c *n.Context) richtext.Attributes {
	tag := inlineTagAttachment(c, string(slashGoal), goalTargetIcon, 14)
	tag.Data = slashGoal
	return richtext.Attributes{Attachment: tag}
}

var goalTargetIcon = svg("M12.9 5.25a6.16 6.16 0 1 0 2.73 3.17M9.56 7.27a3.08 3.08 0 1 0 3.17 3.17M9.56 10.44 16.16 3.84m-3.52 0h3.52v3.52")

const (
	goalControlHeight = 28
	goalLineHeight    = 20
	goalIconSize      = 16
	goalActionInset   = 7
	goalActionWidth   = 24 + 2*goalActionInset
	// A one-DIP optical correction brings the icon and text ink to the editor's guides.
	goalContentInset = processingPanelInset - 1
)

func goalElapsed(seconds int64) string {
	if seconds >= 3600 {
		return fmt.Sprintf("%d 小时 %d 分", seconds/3600, seconds%3600/60)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%d 秒", seconds)
}

func goalStatusLabel(state domain.GoalState) string {
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

func (v *View) sessionGoal(c *n.Context, t *n.Theme) {
	m := v.Model
	session := v.contextSession()
	goal := session.Goal
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		return
	}
	key := m.WorkKey(session.ID, "")
	root := v.splitRoot()
	controllable := m.WorkerMinor >= protocol.GoalMinor && m.Connected && v.Controller != nil &&
		m.RunReadiness().Capabilities.Goals && !session.Archived && m.ArchivedContext() == ""
	busy := root.goalActions[key]
	canResume := goal.CanResume()
	canPause := goal.Active()
	resumeDisabled := busy || !session.Status.CanSubmit() || session.ContextChanged || session.ExecutionGuard != "" || m.HasPendingInput()
	rowContext := c.Key("goal-row:" + key.DeviceID + ":" + key.SessionID)
	row := n.Row(rowContext).FillWidth().Height(32).PaddingX(goalContentInset).Margin(1, 0, 2, 0).
		AlignItems(n.Center).Gap(8).Background(t.Background).KeepFocus().Label("目标状态行")
	show := controllable && (row.Hovered() || row.FocusWithin())
	visible := float32(0)
	if show {
		visible = 1
	}
	if !c.Preferences().ReduceMotion {
		visible = row.AnimateTo("actions", visible, motionAnimation(c, 160*time.Millisecond))
	}
	width := float32(goalActionWidth)
	if canPause {
		width += goalActionWidth
	}
	status := goalStatusLabel(goal.State)
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	row.Children(func() {
		summary := n.Column(rowContext.Key("summary")).Grow(1).MinWidth(0).Height(goalControlHeight).Justify(n.Center).
			Focusable().Label("目标摘要").Description(status + "，" + goal.Objective).Tooltip(goal.Objective)
		summary.FocusRing(summary.FocusVisible()).Radius(6)
		summary.Children(func() {
			n.Row(c).FillWidth().Height(goalLineHeight).AlignItems(n.Baseline).Gap(6).Clip().Children(func() {
				inlineSymbol(c, goalTargetIcon, "目标", font, goalIconSize, t.Text).Shrink(0).MarginEnd(-3)
				n.Text(c, "目标").FontSize(12).FixedLineHeight(goalLineHeight).TextColor(t.Text).Shrink(0).Role(n.RoleNone)
				n.Text(c, status).FontSize(12).FixedLineHeight(goalLineHeight).TextColor(t.TextMuted).Shrink(0).Role(n.RoleNone)
				n.Text(c, "·").FontSize(12).FixedLineHeight(goalLineHeight).TextColor(t.TextMuted).Shrink(0).Role(n.RoleNone)
				n.Text(c, goal.Objective).FontSize(12).FixedLineHeight(goalLineHeight).TextColor(t.TextMuted).
					SingleLine().Grow(1).MinWidth(0).MarginStart(4).Role(n.RoleNone)
			})
		})
		// Align the rightmost label with the composer while letting its hover surface extend past the text guide.
		n.Row(c).Shrink(0).MarginEnd(-goalActionInset).AlignItems(n.Center).Children(func() {
			n.Text(c, goalElapsed(goal.ElapsedSeconds)).FontSize(12).FixedLineHeight(goalLineHeight).FontFeatures("tnum").
				TextColor(t.TextMuted).Shrink(0).MarginEnd(goalActionInset)
			if canResume {
				if goalButton(rowContext.Key("resume"), t, "恢复", !controllable || resumeDisabled).Clicked() {
					v.controlGoal(key, session, domain.ResumeGoal)
				}
			}
			if visible > 0 {
				actions := n.Column(rowContext.Key("actions")).Width(width * visible).Height(goalControlHeight).Shrink(0).
					IgnoreInput(!show).AccessHidden(!show)
				if visible < 1 {
					actions.Clip()
				}
				actions.Children(func() {
					n.Row(c).Absolute().Left(0).Top(0).Width(width).Height(goalControlHeight).
						AlignItems(n.Center).Transform(n.Translation(14*(1-visible), 0)).Children(func() {
						if canPause && goalButton(rowContext.Key("pause"), t, "暂停", busy).Clicked() {
							v.controlGoal(key, session, domain.PauseGoal)
						}
						if goalButton(rowContext.Key("clear"), t, "清除", busy).Clicked() {
							v.controlGoal(key, session, domain.ClearGoal)
						}
					})
				})
			}
		})
	})
}

func goalButton(c *n.Context, t *n.Theme, label string, disabled bool) n.Element {
	const verticalInset = (goalControlHeight - goalLineHeight) / 2
	return control(c).Width(goalActionWidth).Height(goalControlHeight).Shrink(0).
		Justify(n.Center).AlignItems(n.Baseline).Padding(verticalInset, goalActionInset).Disabled(disabled).Label(label).Children(func() {
		ink := t.Text
		if disabled {
			ink = t.TextMuted
		}
		n.Text(c, label).FontSize(12).FixedLineHeight(goalLineHeight).TextColor(ink).Role(n.RoleNone)
	})
}

func (v *View) controlGoal(key app.WorkKey, session domain.Session, action domain.GoalAction) {
	root := v.splitRoot()
	if root.goalActions == nil {
		root.goalActions = make(map[app.WorkKey]bool)
	}
	if root.goalActions[key] {
		return
	}
	root.goalActions[key] = true
	connection := v.Model.ConnectionID()
	in := domain.GoalRequest{OperationID: domain.NewID(), SessionID: session.ID, Target: *session.Goal, Action: action}
	if !v.do(app.Job{Method: "session.goal.control", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		delete(root.goalActions, key)
		message := "目标操作尚未确认"
		if err != nil {
			message = err.Error()
		} else {
			var op domain.Operation
			if json.Unmarshal(b, &op) == nil && op.ID == in.OperationID && op.SessionID == in.SessionID {
				if op.State == domain.OperationConfirmed {
					return
				}
				if op.Error != "" {
					message = op.Error
				}
			}
		}
		m.ReportFeedback(app.Feedback{ID: in.OperationID, ConnectionID: connection, WorkspaceID: session.WorkspaceID, SessionID: session.ID, Title: "目标操作未完成", Body: message})
	}}) {
		delete(root.goalActions, key)
	}
}

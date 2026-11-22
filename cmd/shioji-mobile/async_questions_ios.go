package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func asyncQuestionStatuses(items []domain.Item, model *app.Model, session string, interactive bool) map[string]string {
	statuses := make(map[string]string)
	for _, item := range items {
		if item.ReplyTo != "" && (item.Status == "sent" || item.Status == "dispatching" || item.Status == "unknown") {
			statuses[item.ReplyTo] = item.Status
		}
	}
	if interactive {
		for _, input := range model.Pending {
			if input.SessionID != session || input.QuestionReply == nil || statuses[input.QuestionReply.QuestionID] == "sent" {
				continue
			}
			status := "dispatching"
			if model.UncertainSubmissions[input.OperationID] {
				status = "unknown"
			}
			statuses[input.QuestionReply.QuestionID] = status
		}
	}
	return statuses
}

func (a *mobileApp) asyncQuestionMessage(c *ui.Context, parent *mygo.Window, session domain.Session, item domain.Item, statuses map[string]string, interactive bool) {
	p := palette(c)
	m := a.model
	editable := interactive && item.Final && !session.Archived && !session.ReadOnlyHistory() && !a.workspace(session.WorkspaceID).Archived
	canSend := editable && a.controller != nil && m.CanReplyToQuestion(session.ID, session.ThreadID) &&
		!a.sessionStop(session).pending() && !a.draftSaveFailed()
	ui.Column(c.Key("async-questions")).FillWidth().Gap(transcriptTurnGap).Children(func() {
		for _, question := range item.Questions {
			row := c.Key(question.ID)
			ui.Column(row).FillWidth().Gap(panelContentGap).Padding(panelInset).Radius(panelRadius).
				Background(p.panel).Clip().Transition(mobilePanelTransition).Children(func() {
				label(row.Key("question"), question.Text, bodyTextSize, p.ink).Selectable()
				switch statuses[question.ID] {
				case "sent":
					label(row.Key("status"), "已回答", captionTextSize, p.secondary).Role(ui.RoleStatus)
					return
				case "dispatching":
					label(row.Key("status"), "正在提交答案", captionTextSize, p.secondary).Role(ui.RoleStatus)
					return
				case "unknown":
					label(row.Key("status"), "答案待核对", captionTextSize, p.secondary).Role(ui.RoleStatus)
					return
				}
				if !editable || question.Secret {
					for i, option := range question.Options {
						label(row.Key(i), option.Label, controlTextSize, p.secondary)
					}
					return
				}
				answerKey := app.QuestionAnswerKey(m.Snapshot.DeviceID, session.ID, session.ThreadID, item.ID, question.ID)
				sourceKey := "source:" + answerKey
				customKey := "custom:" + answerKey
				answer, source := m.Answers[answerKey], m.Answers[sourceKey]
				if source == "" && len(question.Options) > 0 {
					answer, source = question.Options[0].Label, "preset"
				}
				update := func(value, source string) {
					if a.model != m || !a.visibleLinkSource(parent, session) {
						return
					}
					m.Answers[answerKey], m.Answers[sourceKey] = value, source
					if source == "custom" {
						m.Answers[customKey] = value
					}
					a.save()
				}
				for i, option := range question.Options {
					mobileChoiceRow(row.Key(i), option.Label, option.Description, source == "preset" && answer == option.Label,
						func() { update(option.Label, "preset") })
				}
				if question.Custom && len(question.Options) != 0 {
					mobileChoiceRow(row.Key("custom"), "自定义答案", "", source == "custom", func() {
						if source != "custom" {
							update(m.Answers[customKey], "custom")
						}
					})
				}
				if source == "custom" || len(question.Options) == 0 {
					mobileTextArea(row.Key("answer"), &answer, "自定义答案内容").Height(68).Placeholder("输入你的答案").
						Padding(8, 10).Radius(9).Background(p.canvas).OnChange(func() { update(answer, "custom") })
				}
				primaryAction(row.Key("submit"), "提交答案", func() {
					if a.model != m || !a.visibleLinkSource(parent, session) || a.sessionStop(session).pending() || a.draftSaveFailed() {
						return
					}
					input, reply, err := m.BeginQuestionReply(session.ID, session.ThreadID, item.ID, question.ID, answer)
					if err != nil {
						m.Error = err.Error()
						a.refresh()
						return
					}
					a.deliverInput(input, "question.reply", reply, "答案未发送，填写内容已保留")
				}).KeepFocus().Disabled(!canSend || strings.TrimSpace(answer) == "")
			})
		}
	})
}

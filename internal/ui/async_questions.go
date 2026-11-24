package ui

import (
	"encoding/json"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type asyncQuestionOptionsKey struct{}

func (v *View) asyncQuestionMessage(c *n.Context, t *n.Theme, item domain.Item) {
	m := v.Model
	session := m.SessionID
	status := map[string]string{}
	for _, i := range m.Details[session].Items {
		if i.ReplyTo != "" && (i.Status == "sent" || i.Status == "dispatching" || i.Status == "unknown") {
			status[i.ReplyTo] = i.Status
		}
	}
	canReply := item.Final && v.Controller != nil && m.CanReplyToQuestion(session, m.Details[session].Session.ThreadID) && !v.sessionStopInFlight(session)
	group := domain.PendingRequest{SessionID: session, ID: "async:" + item.ID, Questions: item.Questions}
	root := n.Column(c.Key(item.ID)).FillWidth().Clip()
	v.transcriptContentHeight(c, root, true, true, 180*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().Gap(8).Children(func() {
			for i, question := range item.Questions {
				card := n.Column(c.Key(question.ID)).FillWidth().Gap(8).Padding(10, 12).
					Background(raised(t)).Border(1, t.Border).Radius(requestRadius)
				expanded := n.Local(card, asyncQuestionOptionsKey{}, func() map[string]bool { return map[string]bool{} })
				card.Children(func() {
					switch status[question.ID] {
					case "sent", "dispatching", "unknown":
						n.Text(c, question.Text).FontSize(13).FixedLineHeight(20)
						label := map[string]string{"sent": "已回答", "dispatching": "正在提交答案", "unknown": "答案待核对"}[status[question.ID]]
						n.Text(c, label).FontSize(11).TextColor(t.TextMuted).Role(n.RoleStatus)
					default:
						if question.Secret {
							n.Text(c, question.Text).FontSize(13).FixedLineHeight(20)
							n.Text(c, "此问题需要保密输入。请在 Agent 的原生界面回答。").
								FontSize(12).TextColor(t.Warning).Role(n.RoleStatus)
							return
						}
						page := requestPage{request: group, question: i}
						v.questionField(c, t, group, question, (*expanded)[page.key()])
						n.Row(c).FillWidth().Gap(8).Margin(4, 0, 0).AlignItems(n.Center).Children(func() {
							v.questionSelectionControls(c, t, page, *expanded, nil)
							n.Spacer(c).Grow(1)
							text := strings.TrimSpace(v.questionAnswer(group, question))
							if controlFeedback(c, n.PrimaryButton(c.Key("submit-answer"), "提交答案").Radius(requestRadius).Height(processingPanelActionHeight).FontSize(12).Disabled(!canReply || text == ""), requestRadius, true).Clicked() {
								v.sendAsyncQuestionReply(item.ID, question.ID, text)
							}
						})
					}
				})
			}
		})
		return 0
	})
}

func (v *View) sendAsyncQuestionReply(item, question, text string) {
	m := v.Model
	in, reply, err := m.BeginQuestionReply(m.SessionID, m.Details[m.SessionID].Session.ThreadID, item, question, text)
	if err != nil {
		m.Error = err.Error()
		return
	}
	var prepare func() error
	if v.Saver != nil {
		state, _ := v.persistenceModel().EncodeState()
		v.Saver.Queue(state)
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Prepare: prepare, Method: "question.reply", Input: reply, Apply: func(m *app.Model, data json.RawMessage, err error) {
		var op domain.Operation
		if err == nil {
			err = json.Unmarshal(data, &op)
		}
		if err != nil {
			m.SubmissionError(in, err)
			return
		}
		m.Accepted(in, op)
	}}) {
		m.Reject(in, "操作繁忙，请重试")
	}
}

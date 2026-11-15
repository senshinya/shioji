package ui

import (
	"encoding/json"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"net/url"
	"shioji/internal/domain"
	"strings"
)

func requestKey(r domain.PendingRequest, id string) string {
	return r.SessionID + "/" + r.ID + "/" + id
}
func (v *View) pruneSecretAnswers(detail domain.SessionDetail) {
	if v.secretAnswers == nil {
		v.secretAnswers = map[string]string{}
		v.secretBindings = map[string]*editBinding{}
	}
	for key := range v.secretAnswers {
		keep := false
		for _, r := range detail.Requests {
			if (r.State.AwaitingResolution()) && strings.HasPrefix(key, r.SessionID+"/"+r.ID+"/") {
				keep = true
				break
			}
		}
		if strings.HasPrefix(key, detail.Session.ID+"/") && !keep {
			delete(v.secretAnswers, key)
			delete(v.secretBindings, key)
		}
	}
}
func (v *View) requestBody(c *n.Context, t *n.Theme, r domain.PendingRequest, question int) {
	if question >= 0 {
		v.questionField(c, t, r, r.Questions[question])
		return
	}
	if r.Notice != "" && r.Kind != "external" {
		n.Text(c, r.Notice).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
	}
	if r.Text != "" {
		n.Text(c, r.Text).FillWidth().FontSize(12).FixedLineHeight(20).Selectable()
	}
	switch r.Kind {
	case "form":
		for _, f := range r.Fields {
			v.formField(c.Key(f.ID), t, r, f)
		}
	case "permissions":
		selected := v.requestSelection(r, "permissions", r.Choices)
		for _, choice := range r.Choices {
			if choice.Restriction {
				n.Text(c, choice.Label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				continue
			}
			checked := containsSelection(selected, choice.ID)
			if controlFeedback(c, n.Checkbox(c.Key(choice.ID), &checked, choice.Label).FillWidth().MinHeight(28).FontSize(12), 4).Changed() {
				selected = toggleSelection(selected, choice.ID, checked)
				v.saveRequestSelection(r, "permissions", selected)
			}
		}
	case "external":
		if r.URL != "" {
			n.Text(c, r.URL).FontSize(12).FixedLineHeight(20).Selectable()
		}
		if r.Notice != "" {
			n.Text(c, r.Notice).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
	case "approval":
		for _, item := range v.Model.Details[r.SessionID].Items {
			if item.ID == r.ItemID {
				for _, file := range item.Files {
					n.Text(c, file.Path).FontSize(12).FixedLineHeight(20).Selectable()
				}
			}
		}
	}
}
func (v *View) questionField(c *n.Context, t *n.Theme, r domain.PendingRequest, q domain.Question) {
	key := requestKey(r, q.ID)
	n.Text(c, q.Text).FontSize(13).FixedLineHeight(20)
	if q.Secret {
		binding := textBinding(v.secretBindings, key, v.secretAnswers[key])
		input := textField(c.Key("secret:"+key), &binding.Text).Password().FillWidth().Height(32).Padding(5, 8).FontSize(12).Label("敏感答案")
		if input.Changed() {
			v.secretAnswers[key] = binding.Text
			binding.LastModel = binding.Text
		}
		return
	}
	selected := textBinding(v.answerBindings, key, v.Model.Answers[key])
	if len(q.Options) > 0 {
		n.Column(c).FillWidth().Children(func() {
			for i, option := range q.Options {
				if i > 0 {
					processingPanelDivider(c, t)
				}
				controlFeedback(c, n.Radio(c.Key(option.Label), &selected.Text, option.Label, option.Label).FillWidth().MinHeight(32).FontSize(12).Label(option.Label).Tooltip(option.Description), 4).Changed()
				if option.Description != "" {
					n.Text(c, option.Description).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Padding(0, 0, 6, 22)
				}
			}
		})
	}
	v.Model.Answers[key] = selected.Text
	selected.LastModel = selected.Text
	if q.Custom || len(q.Options) == 0 {
		custom := textBinding(v.customBindings, key, v.Model.Answers["custom:"+key])
		input := n.TextAreaBase(c.Key("answer:"+key), &custom.Text).FillWidth().Lines(1, 4).MinHeight(32).MaxHeight(96).Padding(5, 8).FontSize(12).FixedLineHeight(20).Background(raised(t)).Border(1, t.Border).Radius(requestRadius).Label("自定义答案").Placeholder("输入答案…").PlaceholderColor(subtle(t))
		fieldFeedback(c, input)
		if input.Changed() {
			v.Model.Answers["custom:"+key] = custom.Text
			v.Model.Answers[key] = custom.Text
			custom.LastModel = custom.Text
		}
	}
}
func (v *View) formField(c *n.Context, t *n.Theme, r domain.PendingRequest, f domain.FormField) {
	key := requestKey(r, f.ID)
	if _, set := v.Model.Answers[key]; !set {
		if len(f.Default) > 0 {
			v.saveRequestSelection(r, f.ID, f.Default)
		} else if f.Type == "array" && f.Required {
			v.saveRequestSelection(r, f.ID, []string{})
		} else if f.Type == "boolean" {
			v.saveRequestSelection(r, f.ID, []string{"false"})
		}
	}
	values := v.requestSelection(r, f.ID, nil)
	n.Column(c).FillWidth().Gap(4).Children(func() {
		title := f.Title
		if f.Required {
			title += " *"
		}
		n.Text(c, title).FontSize(12).FixedLineHeight(20)
		if f.Description != "" {
			n.Text(c, f.Description).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
		}
		if f.Type == "boolean" {
			checked := len(values) == 1 && values[0] == "true"
			if controlFeedback(c, n.Checkbox(c, &checked, f.Title).MinHeight(28).FontSize(12), 4).Changed() {
				value := "false"
				if checked {
					value = "true"
				}
				v.saveRequestSelection(r, f.ID, []string{value})
			}
			return
		}
		if len(f.Options) > 0 {
			n.Column(c).FillWidth().Children(func() {
				for i, option := range f.Options {
					if i > 0 {
						processingPanelDivider(c, t)
					}
					if f.Type == "array" {
						checked := containsSelection(values, option.Value)
						if controlFeedback(c, n.Checkbox(c.Key(option.Value), &checked, option.Label).FillWidth().MinHeight(32).FontSize(12).Label(option.Label), 4).Changed() {
							values = toggleSelection(values, option.Value, checked)
							v.saveRequestSelection(r, f.ID, values)
						}
					} else {
						value := ""
						if len(values) > 0 {
							value = values[0]
						}
						if controlFeedback(c, n.Radio(c.Key(option.Value), &value, option.Value, option.Label).FillWidth().MinHeight(32).FontSize(12).Label(option.Label), 4).Changed() {
							values = []string{value}
							v.saveRequestSelection(r, f.ID, values)
						}
					}
				}
			})
			return
		}
		value := ""
		if len(values) > 0 {
			value = values[0]
		}
		binding := textBinding(v.answerBindings, key, value)
		input := textField(c.Key("form:"+key), &binding.Text).FillWidth().Height(32).Padding(5, 8).FontSize(12).Label(f.Title)
		if f.Type == "integer" || f.Type == "number" {
			input.MaxWidth(140)
		}
		if input.Changed() {
			v.saveRequestSelection(r, f.ID, []string{binding.Text})
			binding.LastModel = binding.Text
		}
	})
}
func containsSelection(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func toggleSelection(values []string, value string, selected bool) []string {
	out := []string{}
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	if selected {
		out = append(out, value)
	}
	return out
}
func (v *View) requestSelection(r domain.PendingRequest, id string, defaults []domain.RequestChoice) []string {
	key := requestKey(r, id)
	raw, set := v.Model.Answers[key]
	if !set && defaults != nil {
		values := []string{}
		for _, choice := range defaults {
			if !choice.Restriction {
				values = append(values, choice.ID)
			}
		}
		v.saveRequestSelection(r, id, values)
		return values
	}
	var values []string
	json.Unmarshal([]byte(raw), &values)
	return values
}
func (v *View) saveRequestSelection(r domain.PendingRequest, id string, values []string) {
	b, _ := json.Marshal(values)
	v.Model.Answers[requestKey(r, id)] = string(b)
}
func (v *View) formAnswers(r domain.PendingRequest) map[string][]string {
	answers := map[string][]string{}
	for _, f := range r.Fields {
		if values := v.requestSelection(r, f.ID, nil); len(values) > 0 || f.Type == "array" && f.Required {
			answers[f.ID] = values
		}
	}
	return answers
}
func (v *View) requestActions(c *n.Context, t *n.Theme, r domain.PendingRequest) {
	disabled := !v.Model.Connected || r.State != "pending" || v.requestAction(r).busy
	n.Row(c).FillWidth().Wrap().Gap(messageFooterGap).Children(func() {
		action := func(label, decision string, primary bool, answers map[string][]string, extraDisabled bool) {
			var button n.Element
			if primary {
				button = controlFeedback(c, n.PrimaryButton(c, label).Radius(requestRadius).Height(28).FontSize(12).Disabled(disabled || extraDisabled), requestRadius, true)
			} else {
				button = ghost(c, label, requestRadius).Disabled(disabled || extraDisabled)
			}
			if button.Clicked() {
				v.respond(r, domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: decision, Answers: answers})
			}
		}
		switch r.Kind {
		case "approval":
			choices := r.Choices
			if len(choices) == 0 && r.Notice == "" {
				choices = []domain.RequestChoice{{ID: "accept", Label: "允许这一次"}, {ID: "decline", Label: "拒绝"}}
			}
			for i, choice := range choices {
				action(choice.Label, choice.ID, i == 0, nil, false)
			}
		case "permissions":
			selected := v.requestSelection(r, "permissions", r.Choices)
			answers := map[string][]string{"permissions": selected}
			action("允许本轮", "turn", true, answers, len(selected) == 0)
			action("允许本会话", "session", false, answers, len(selected) == 0)
			action("拒绝", "decline", false, nil, false)
		case "form":
			answers := v.formAnswers(r)
			_, err := domain.FormContent(r.Fields, answers)
			action("确认表单", "accept", true, answers, err != nil)
			action("拒绝", "decline", false, nil, false)
			action("取消", "cancel", false, nil, false)
		case "external":
			if r.URL != "" {
				u, err := url.Parse(r.URL)
				safe := err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
				if controlFeedback(c, n.PrimaryButton(c, "在浏览器中继续").Radius(requestRadius).Height(28).FontSize(12).Disabled(disabled || !safe), requestRadius, true).Clicked() {
					if err := mygo.Shell.OpenExternal(r.URL); err != nil {
						v.Model.Error = err.Error()
					}
				}
			}
			action("拒绝", "decline", false, nil, false)
			action("取消", "cancel", false, nil, false)
		default:
			answers := map[string][]string{}
			complete := true
			for _, q := range r.Questions {
				value := v.Model.Answers[requestKey(r, q.ID)]
				if q.Secret {
					value = v.secretAnswers[requestKey(r, q.ID)]
				}
				if strings.TrimSpace(value) == "" {
					complete = false
				}
				answers[q.ID] = []string{value}
			}
			action("提交答案", "", true, answers, !complete)
		}
	})
}

func (v *View) openSourceRequest(item domain.Item) {
	page := 0
	for _, r := range v.Model.Details[v.Model.SessionID].Requests {
		if item.ID == "request:"+r.ID {
			if r.State.AwaitingResolution() {
				v.Model.Pager[v.Model.SessionID] = page
				v.Model.Collapsed[v.Model.SessionID] = false
			}
			return
		}
		if r.State.AwaitingResolution() {
			page += max(1, len(r.Questions))
		}
	}
}
func (v *View) requestResult(c *n.Context, t *n.Theme, item domain.Item) {
	v.requestResultWithStream(c, t, item, v.replyStreams[item.ID], true)
}
func (v *View) requestResultWithStream(c *n.Context, t *n.Theme, item domain.Item, stream *replyStream, origin bool) {
	progress := float32(1)
	if stream != nil {
		if stream.live() && !c.Preferences().ReduceMotion {
			progress = n.EaseOut(min(1, max(0, float32(c.Now().Sub(stream.changedAt))/float32(receiptEntranceDuration))))
		}
		stream.mount()
		stream.seen, stream.revealing = v.replyFrame, progress < 1
		if stream.revealing {
			c.AnimationFrame()
		}
	}
	// A newly virtualized row is born with all its parents, so an element
	// Enter transition is deliberately skipped by MyGo. Drive live receipts
	// from their arrival clock instead; old/remounted rows stay settled.
	n.Column(c.Key("request-result")).FillWidth().Padding(0, 0, 0, 12).Gap(8).Label("回答结果").Opacity(progress).Transform(n.Translation(0, 6*(1-progress))).Draw(func(p *n.Painter, r n.Rect) {
		p.Fill(n.Rect{X: r.X, Y: r.Y + 2, W: 1, H: max(0, r.H-4)}, t.Border, 0)
	}).Children(func() {
		if origin && item.WorkID != "" {
			v.requestOrigin(c, t, domain.PendingRequest{WorkID: item.WorkID})
		}
		for _, part := range item.Parts {
			n.Column(c).FillWidth().Gap(2).Children(func() {
				if part.Label != "" {
					n.Text(c, part.Label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
				}
				n.Text(c, part.Text).FontSize(13).FixedLineHeight(20).Selectable()
			})
		}
	})
}

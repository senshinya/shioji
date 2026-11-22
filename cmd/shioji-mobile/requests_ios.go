package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type requestPanelState struct {
	mobileRequest
	expanded, accepted bool
	file               string
	values             map[string][]string
	text               map[string]string
	custom             map[string]bool
}

type mobileRequestReveal struct {
	scope app.WorkKey
	id    string
}

func (a *mobileApp) focusRequest(request domain.PendingRequest) {
	a.requestReveal = mobileRequestReveal{scope: a.model.WorkKey(request.SessionID, request.WorkID), id: request.ID}
}

func requestName(request domain.PendingRequest) string {
	if request.Title != "" {
		return request.Title
	}
	if len(request.Questions) != 0 || len(request.Fields) != 0 {
		return "回答问题"
	}
	return "待处理请求"
}

func pendingRequest(request domain.PendingRequest) bool {
	return request.State == "" || request.State.AwaitingResolution()
}

func (a *mobileApp) requestSource(c *ui.Context, parent *mygo.Window, sid string, item domain.Item,
	requests []domain.PendingRequest, interactive bool, reveal func()) {
	p := palette(c)
	if interactive {
		for _, request := range requests {
			if request.SessionID == sid && request.WorkID == item.WorkID && item.ID == "request:"+request.ID && pendingRequest(request) {
				title := requestName(request)
				button := transcriptRequestAction(c, title, "查看请求来源", permissionSymbol, rightSymbol, func() {
					a.focusRequest(request)
					if reveal != nil {
						reveal()
					}
					a.refresh()
				}).Value(title)
				transcriptDisclosure(button)
				return
			}
		}
	}
	title := item.Name
	if title == "" {
		title = item.Text
	}
	switch domain.RequestState(item.Status) {
	case domain.RequestAnswered:
		if domain.HasInlineRequestReceipt(item.Presentation) && len(item.Parts) > 0 {
			a.inlineRequestReceipt(c, parent, sid, item, title)
			return
		}
		title += " · 已处理"
	case domain.RequestResolved:
		title += " · 已结束"
	case domain.RequestInvalidated:
		title += " · 已失效"
	case domain.RequestSubmitting:
		title += " · 提交中"
	}
	label(c, title, captionTextSize, p.secondary).Padding(transcriptItemGap, 0)
}

func (a *mobileApp) inlineRequestReceipt(c *ui.Context, parent *mygo.Window, sid string, item domain.Item, title string) {
	scope := a.model.WorkKey(sid, item.WorkID)
	expanded := ui.Local(c.Root(), struct {
		receipt app.WorkKey
		item    string
	}{scope, item.ID}, func() bool { return false })
	parts, glyph := item.Parts, permissionSymbol
	if item.Presentation == "questions" {
		glyph = infoSymbol
		title = "已回答"
		if len(parts) > 1 {
			title = fmt.Sprintf("已回答 %d 个问题", len(parts))
		} else if !*expanded {
			if summary := strings.Join(strings.Fields(parts[0].Text), " "); summary != "" {
				title += " · " + summary
			}
		}
	} else {
		decision := strings.TrimSpace(parts[0].Text)
		if decision == "" {
			decision = "已处理"
		}
		title += " · " + decision
		parts = parts[1:]
	}
	if len(parts) == 0 {
		ink := palette(c).secondary
		font := mobileStatusFont(c, captionTextSize)
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Padding(transcriptItemGap, 0).Children(func() {
			transcriptSymbol(c, glyph, title, font, 14, ink)
			label(c, title, captionTextSize, ink).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).MinWidth(0)
		})
		return
	}
	chevron := rightSymbol
	if *expanded {
		chevron = downSymbol
	}
	ui.Column(c.Key("request-receipt")).Children(func() {
		button := transcriptRequestAction(c.Key("summary"), title, title, glyph, chevron,
			func() { *expanded = !*expanded }).Expanded(*expanded)
		transcriptDisclosure(button)
		if *expanded {
			body := c.Key("details")
			ui.Column(body).Gap(6).Padding(0, 0, 5, transcriptGlyphSlot+6).Clip().Transition(mobilePanelTransition).Children(func() {
				a.contentParts(body, parent, a.sessionByID(sid), parts)
			})
		}
	})
}

func transcriptRequestAction(c *ui.Context, title, accessible string, glyph, arrow *ui.SVG, click func()) ui.Element {
	font := mobileStatusFont(c, captionTextSize)
	ink := palette(c)
	return mobileButton(c, accessible, click).Gap(6).AlignItems(ui.Baseline).Children(func() {
		transcriptSymbol(c, glyph, title, font, 14, ink.secondary)
		oneLine(c, title, captionTextSize, ink.ink).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).Shrink(1)
		transcriptSymbol(c, arrow, title, font, 12, ink.secondary)
	})
}

func (a *mobileApp) requestPanels(c *ui.Context, parent *mygo.Window, sid, workID string, scrollOnReveal bool) {
	if parent == nil {
		return
	}
	scope := a.model.WorkKey(sid, workID)
	selected := ui.Local(c.Root(), struct{ requests app.WorkKey }{scope}, func() string { return "" })
	requests := make([]domain.PendingRequest, 0, 1)
	index, revealIndex := 0, -1
	for _, request := range a.model.Snapshot.PendingRequests {
		if request.SessionID == sid && request.WorkID == workID && pendingRequest(request) {
			if request.ID == *selected {
				index = len(requests)
			}
			if a.requestReveal.scope == scope && request.ID == a.requestReveal.id {
				revealIndex = len(requests)
			}
			requests = append(requests, request)
		}
	}
	if len(requests) == 0 {
		return
	}
	reveal := revealIndex >= 0
	if reveal {
		index = revealIndex
		a.requestReveal = mobileRequestReveal{}
	}
	request := requests[index]
	*selected = request.ID
	key := struct {
		device, session, work, request string
		revision                       int64
	}{a.model.Snapshot.DeviceID, sid, workID, request.ID, request.Revision}
	state := ui.Local(c.Root(), key, func() requestPanelState {
		s := requestPanelState{expanded: true, values: make(map[string][]string), text: make(map[string]string), custom: make(map[string]bool)}
		for _, field := range request.Fields {
			s.values[field.ID] = slices.Clone(field.Default)
			if len(field.Options) == 0 && len(field.Default) == 1 {
				s.text[field.ID] = field.Default[0]
			}
		}
		return s
	})
	if reveal {
		state.expanded = true
	}
	respond := func(decision string) {
		if state.accepted || request.State == domain.RequestSubmitting {
			return
		}
		answer := domain.Answer{RequestID: request.ID, Revision: request.Revision, Decision: decision}
		if decision == "" {
			answer.Answers = make(map[string][]string)
			for id, values := range state.values {
				if len(values) != 0 {
					answer.Answers[id] = slices.Clone(values)
				}
			}
			for id, value := range state.text {
				if text := strings.TrimSpace(value); text != "" {
					answer.Answers[id] = []string{text}
				}
			}
		}
		a.pageJob(parent, &state.mobileRequest, "request.respond", protocol.AnswerInput{SessionID: sid, Answer: answer}, func(_ json.RawMessage) error {
			state.accepted = true
			return nil
		})
	}
	p := palette(c)
	row := c.Key("session-request")
	panel := mobilePanel(row).Clip()
	if reveal && scrollOnReveal {
		panel.ScrollIntoView(ui.ScrollIntoViewOptions{Animation: &ui.Smooth})
	}
	panel.Children(func() {
		ui.Row(row).AlignItems(ui.Center).Children(func() {
			title, accessible := requestName(request), requestName(request)
			if request.Title == "允许执行此命令？" {
				accessible = "命令审批"
			}
			if len(request.Questions) > 0 {
				title, accessible = request.Questions[0].Header, "回答问题"
				if title == "" {
					title = requestName(request)
				}
			}
			arrow := upSymbol
			if state.expanded {
				arrow = downSymbol
			}
			ui.ButtonBase(row).Grow(1).MinWidth(0).MinHeight(mobileTapSize(c)).PaddingX(panelInset).
				AlignItems(ui.Center).Role(ui.RoleButton).Label(accessible).Expanded(state.expanded).KeepFocus().
				OnClick(func() { state.expanded = !state.expanded }).Children(func() {
				font := mobileStatusFont(row, 13)
				font.Weight = 630
				ui.Row(row.Key("title-line")).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Baseline).PassThrough().Children(func() {
					transcriptSymbol(row, permissionSymbol, title, font, 16, p.ink)
					label(row, title, 13, p.ink).Font(font.Family).FontWeight(font.Weight).
						FixedLineHeight(mobileStatusLineHeight(font)).Grow(1).MinWidth(0).PassThrough()
					transcriptSymbol(row, arrow, title, font, 14, p.ink)
				})
			})
			if len(requests) > 1 {
				previous := (index + len(requests) - 1) % len(requests)
				next := (index + 1) % len(requests)
				iconButton(row, "上一条请求", backSymbol, 14, func() { *selected = requests[previous].ID }).KeepFocus()
				oneLine(row, fmt.Sprintf("%d/%d", index+1, len(requests)), 12, p.secondary)
				iconButton(row, "下一条请求", rightSymbol, 14, func() { *selected = requests[next].ID }).KeepFocus()
			}
		})
		mobileDisclosureBody(row.Key(request.ID), state.expanded, func(content *ui.Context) {
			disabled := state.busy || state.accepted || !a.model.Connected || request.State == domain.RequestSubmitting
			ui.Column(content).Padding(0, panelInset, panelInset).Gap(panelContentGap).Children(func() {
				fileApproval := request.Title == "允许修改这些文件？" || request.Kind == "file-approval"
				if fileApproval {
					label(content, "文件变更", captionTextSize, p.secondary)
					a.requestFiles(content, parent, request, state)
				}
				if request.Kind == "permissions" {
					label(content, "权限请求", captionTextSize, p.secondary)
				}
				if request.Text != "" {
					command, context, _ := strings.Cut(request.Text, "\n")
					ui.Scroll(content).MaxHeight(144).Radius(10).Background(p.canvas).Children(func() {
						ui.Column(content).Padding(8, 10).Gap(3).Children(func() {
							label(content, command, 13, p.ink).Font(a.codeFont()).Selectable()
							if context != "" {
								label(content, context, 13, p.ink).Font("monospace").Selectable()
							}
						})
					})
				}
				if request.Notice != "" {
					label(content, request.Notice, 12, p.secondary)
				}
				if len(request.Questions) != 0 || len(request.Fields) != 0 {
					ui.Scroll(content.Key("fields")).MaxHeight(228).Children(func() {
						ui.Column(content).Gap(5).Children(func() { requestFields(content, request, state) })
					})
					unanswered := false
					for _, question := range request.Questions {
						if len(state.values[question.ID]) == 0 && strings.TrimSpace(state.text[question.ID]) == "" {
							unanswered = true
						}
					}
					ui.Row(content.Key("question-actions")).Gap(panelContentGap).Children(func() {
						action(content.Key("defer"), "暂不选择", func() { state.expanded = false }).KeepFocus()
						primaryAction(content.Key("submit"), "提交答案", func() { respond("") }).
							Grow(1).Basis(0).Radius(8).KeepFocus().Disabled(disabled || unanswered)
					})
				}
				for first := 0; first < len(request.Choices); {
					columns := 4
					width := c.Root().LayoutBounds().W
					if width > 0 && width < 375 || c.Preferences().TextScale > 1.2 {
						columns = 2
					}
					end := min(first+columns, len(request.Choices))
					ui.Row(content.Key(first)).Gap(6).Children(func() {
						for i, choice := range request.Choices[first:end] {
							fill, ink := p.canvas, p.ink
							if first+i == 0 && !fileApproval && request.Kind != "permissions" || choice.ID == "accept" || choice.ID == "turn" {
								fill, ink = p.ink, p.canvas
							}
							filledAction(content.Key(choice.ID), choice.Label, fill, ink, func() { respond(choice.ID) }).
								Grow(1).Basis(0).MinWidth(mobileTapSize(c)).Padding(4).Radius(8).KeepFocus().Disabled(disabled)
						}
					})
					first = end
				}
				state.status(content)
			})
		})
	})
}

func (a *mobileApp) requestFiles(c *ui.Context, parent *mygo.Window, request domain.PendingRequest, state *requestPanelState) {
	sid := request.SessionID
	if request.WorkID == "" {
		for _, item := range a.model.Details[sid].Items {
			if item.ID != request.ItemID || item.WorkID != "" {
				continue
			}
			for _, change := range item.Files {
				path := change.Path
				row := c.Key(path)
				button := mobileButton(row, filepath.Base(path), func() {
					review := &reviewState{parent: parent, session: a.sessionByID(sid)}
					if a.wide && parent == a.detail {
						a.replaceReview(review)
					}
					a.showReviewFile(review, path)
				}).KeepFocus()
				mobileRowContent(row, button, filepath.Base(path), filepath.Dir(path), fileCodeSymbol, true, nil)
			}
			return
		}
		return
	}
	detail := a.model.WorkDetails[a.model.WorkKey(sid, request.WorkID)]
	for _, entry := range detail.Items {
		item := entry.Item
		if item.ID != request.ItemID || len(item.Files) == 0 {
			continue
		}
		for _, file := range item.Files {
			row := c.Key(file.Path)
			if file.Patch == "" && file.Destination == "" {
				mobileRowContent(row, ui.Row(row), filepath.Base(file.Path), filepath.Dir(file.Path), fileCodeSymbol, false, nil)
				continue
			}
			expanded := state.file == file.Path
			arrow := rightSymbol
			if expanded {
				arrow = downSymbol
			}
			iconAction(row, file.Path, filepath.Base(file.Path), fileCodeSymbol, 14, arrow, 12, controlTextSize, 400, func() {
				state.file = file.Path
				if expanded {
					state.file = ""
				}
			}).AlignSelf(ui.Start).PaddingX(0)
			// The enclosing request panel owns the changing height.
			if expanded {
				if file.Destination != "" {
					label(row, file.Destination, captionTextSize, palette(row).secondary).Font("monospace").Selectable()
				}
				a.transcriptPatch(row.Key("proposal"), file)
			}
		}
		return
	}
	mobileMessage(c, "此请求的文件变更尚未载入")
}

func requestFields(c *ui.Context, request domain.PendingRequest, state *requestPanelState) {
	p := palette(c)
	for i, question := range request.Questions {
		row := c.Key(question.ID)
		if i != 0 {
			label(row, question.Header, 13, p.ink).FontWeight(600)
		}
		if question.Text != "" {
			label(row, question.Text, 12, p.secondary)
		}
		for _, option := range question.Options {
			choice := row.Key(option.Label)
			mobileChoiceRow(choice, option.Label, option.Description, slices.Contains(state.values[question.ID], option.Label), func() {
				state.values[question.ID] = []string{option.Label}
				delete(state.text, question.ID)
				delete(state.custom, question.ID)
			})
		}
		if question.Custom && len(question.Options) != 0 {
			mobileChoiceRow(row.Key("custom"), "自定义答案", "", state.custom[question.ID], func() {
				state.custom[question.ID] = true
				delete(state.values, question.ID)
			})
		}
		if state.custom[question.ID] || len(question.Options) == 0 {
			text := state.text[question.ID]
			label(row, "答案", 12, p.secondary).Padding(5, 1, 0)
			change := func() { state.text[question.ID] = text; delete(state.values, question.ID) }
			if question.Secret {
				ui.TextInputBase(row, &text).Label("自定义答案内容").Height(mobileTapSize(c)).Placeholder("输入你的答案").PaddingX(10).
					Password().Radius(9).Background(p.canvas).OnChange(change)
			} else {
				mobileTextArea(row, &text, "自定义答案内容").Height(68).Placeholder("输入你的答案").
					Padding(8, 10).Radius(9).Background(p.canvas).OnChange(change)
			}
		}
	}
	for _, field := range request.Fields {
		row := c.Key(field.ID)
		if field.Description != "" {
			label(row, field.Description, 12, p.secondary)
		}
		if len(field.Options) == 0 {
			text := state.text[field.ID]
			formRow(row, field.Title, func() {
				ui.TextInputBase(row, &text).WidthPercent(58).MinHeight(mobileTapSize(c)).Label(field.Title).TextAlign(ui.End).
					OnChange(func() { state.text[field.ID] = text; delete(state.values, field.ID) })
			})
			continue
		}
		label(row, field.Title, 13, p.ink).FontWeight(600)
		for _, option := range field.Options {
			mark := ""
			if slices.Contains(state.values[field.ID], option.Value) {
				mark = "已选"
			}
			mobileRow(row.Key(option.Value), option.Label, "", mark, nil, func() {
				values := state.values[field.ID]
				if field.Type != "array" {
					state.values[field.ID] = []string{option.Value}
				} else if index := slices.Index(values, option.Value); index >= 0 {
					state.values[field.ID] = slices.Delete(values, index, index+1)
				} else {
					state.values[field.ID] = append(values, option.Value)
				}
			})
		}
	}
}

package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"shioji/internal/domain"
)

type elicitationResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

type questionOption struct {
	Const string
	Title string
}

type questionSchema struct {
	Type       string
	Properties map[string]struct {
		Type, Title string
		OneOf       []questionOption
		Items       struct{ AnyOf []questionOption }
	}
}

func (r *runtime) receiveQuestion(msg packet) error {
	var payload struct {
		SessionID, ToolCallID, Mode string
		RequestedSchema             questionSchema
	}
	ctx := context.Background()
	decline := elicitationResponse{Action: "decline"}
	if json.Unmarshal(msg.Params, &payload) != nil || payload.Mode != "form" {
		return r.reply(ctx, msg.ID, decline, nil)
	}
	request, turn, ok := r.requestSource(payload.SessionID, payload.ToolCallID)
	if !ok {
		return r.reply(ctx, msg.ID, decline, nil)
	}
	fields := nativeQuestionFields(payload.RequestedSchema)
	if len(fields) == 0 {
		return r.reply(ctx, msg.ID, decline, nil)
	}
	request.Kind, request.Title, request.Fields = "form", "Antigravity 提问", fields
	return r.openRequest(msg.ID, request, turn)
}

// AGY exposes ask_question as an ACP form with numbered choice fields and
// paired free-text alternatives. Other elicitation schemas are not questions.
func nativeQuestionFields(schema questionSchema) []domain.FormField {
	count := len(schema.Properties)
	if schema.Type != "object" || count == 0 || count%2 != 0 || count > 24 {
		return nil
	}
	fields := make([]domain.FormField, 0, count)
	for i := 1; i <= count/2; i++ {
		key := "q" + strconv.Itoa(i)
		otherKey := key + "_other"
		field, other := schema.Properties[key], schema.Properties[otherKey]
		title := clip(strings.TrimSpace(field.Title), 4096)
		if title == "" || other.Type != "string" || len(other.OneOf) != 0 || field.Type != "string" && field.Type != "array" {
			return nil
		}
		options := field.OneOf
		if field.Type == "array" {
			options = field.Items.AnyOf
		}
		if len(options) < 2 || len(options) > 64 {
			return nil
		}
		choices := make([]domain.FormOption, 0, len(options))
		for j, option := range options {
			label := clip(strings.TrimSpace(option.Title), 512)
			if option.Const != strconv.Itoa(j+1) || label == "" {
				return nil
			}
			choices = append(choices, domain.FormOption{Value: option.Const, Label: label})
		}
		fields = append(fields,
			domain.FormField{ID: key, Title: title, Type: field.Type, Options: choices},
			domain.FormField{ID: otherKey, Title: "其他答案 · " + title, Type: "string"},
		)
	}
	return fields
}

func questionResponse(fields []domain.FormField, answer domain.Answer) (elicitationResponse, error) {
	response := elicitationResponse{Action: answer.Decision}
	switch answer.Decision {
	case "cancel", "decline":
		return response, nil
	case "accept":
	default:
		return response, errors.New("该提问操作不可用")
	}
	content, err := domain.FormContent(fields, answer.Answers)
	if err != nil {
		return response, err
	}
	for i := 0; i < len(fields); i += 2 {
		field, other := fields[i], fields[i+1]
		custom, _ := content[other.ID].(string)
		if strings.TrimSpace(custom) != "" {
			if field.Type == "string" {
				delete(content, field.ID) // For a single choice, free text replaces a selected option.
			}
			continue
		}
		delete(content, other.ID)
		if selected, ok := content[field.ID]; !ok || field.Type == "array" && len(selected.([]string)) == 0 {
			return response, fmt.Errorf("请回答「%s」", field.Title)
		}
	}
	response.Content = content
	return response, nil
}

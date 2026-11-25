package claudecode

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"shioji/internal/domain"
)

// The native reply uses question text and option labels as identifiers. Keep
// those values private to the adapter and give the UI stable per-form IDs.
type nativeQuestion struct {
	Question    string
	Options     []domain.Option
	MultiSelect bool
}

func questionForm(raw json.RawMessage, key string) ([]nativeQuestion, []domain.FormField, error) {
	var input struct{ Questions []nativeQuestion }
	invalid := errors.New("Claude Code 问题格式无效")
	if json.Unmarshal(raw, &input) != nil || len(input.Questions) == 0 || len(input.Questions) > 4 {
		return nil, nil, invalid
	}
	seen := make(map[string]bool)
	var fields []domain.FormField
	for i, q := range input.Questions {
		if strings.TrimSpace(q.Question) == "" || seen[q.Question] || len(q.Options) < 2 || len(q.Options) > 4 {
			return nil, nil, invalid
		}
		seen[q.Question] = true
		id := strconv.Itoa(i)
		field := domain.FormField{ID: id, Title: q.Question, Type: "string"}
		if q.MultiSelect {
			field.Type = "array"
		}
		var descriptions []string
		labels := make(map[string]bool)
		for _, option := range q.Options {
			if strings.TrimSpace(option.Label) == "" || labels[option.Label] {
				return nil, nil, invalid
			}
			labels[option.Label] = true
			field.Options = append(field.Options, domain.FormOption{Value: option.Label, Label: option.Label})
			if option.Description != "" {
				descriptions = append(descriptions, option.Label+" · "+option.Description)
			}
		}
		field.Description = strings.Join(descriptions, "\n")
		if key != "" && strings.Contains(field.Title+field.Description, key) {
			return nil, nil, invalid
		}
		for _, option := range field.Options {
			if key != "" && strings.Contains(option.Value, key) {
				return nil, nil, invalid
			}
		}
		fields = append(fields, field, domain.FormField{ID: id + ":other", Title: "自定义回答", Type: "string"})
	}
	return input.Questions, fields, nil
}

func questionAnswers(p approval, answers map[string][]string) (map[string]string, error) {
	values, err := domain.FormContent(p.request.Fields, answers)
	if err != nil {
		return nil, err
	}
	reply := make(map[string]string, len(p.questions))
	for i, question := range p.questions {
		id := strconv.Itoa(i)
		var selected []string
		switch value := values[id].(type) {
		case string:
			selected = []string{value}
		case []string:
			selected = value
		}
		if other, ok := values[id+":other"].(string); ok && strings.TrimSpace(other) != "" {
			selected = append(selected, other)
		}
		if len(selected) == 0 || !question.MultiSelect && len(selected) != 1 {
			return nil, errors.New("请为每个问题填写答案，单选题只能选择一项或填写自定义回答")
		}
		reply[question.Question] = strings.Join(selected, ", ")
	}
	return reply, nil
}

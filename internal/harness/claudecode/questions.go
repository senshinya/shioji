package claudecode

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"shioji/internal/domain"
)

// The native reply uses question text and option labels as identifiers. Keep
// those values private to the adapter and give the UI stable per-question IDs.
type nativeQuestion struct {
	Question    string          `json:"question"`
	Header      string          `json:"header"`
	Options     []domain.Option `json:"options"`
	MultiSelect bool            `json:"multiSelect"`
}

func questionRequest(raw json.RawMessage, key string) ([]nativeQuestion, []domain.Question, error) {
	var input struct {
		Questions []nativeQuestion `json:"questions"`
	}
	invalid := errors.New("Claude Code 问题格式无效")
	if json.Unmarshal(raw, &input) != nil || len(input.Questions) == 0 || len(input.Questions) > 4 {
		return nil, nil, invalid
	}
	seen := make(map[string]bool)
	questions := make([]domain.Question, 0, len(input.Questions))
	for i, q := range input.Questions {
		if strings.TrimSpace(q.Question) == "" || seen[q.Question] || len(q.Options) < 2 || len(q.Options) > 4 {
			return nil, nil, invalid
		}
		seen[q.Question] = true
		if key != "" && (strings.Contains(q.Question, key) || strings.Contains(q.Header, key)) {
			return nil, nil, invalid
		}
		options := make([]domain.Option, 0, len(q.Options))
		labels := make(map[string]bool)
		for _, option := range q.Options {
			if strings.TrimSpace(option.Label) == "" || labels[option.Label] {
				return nil, nil, invalid
			}
			if key != "" && (strings.Contains(option.Label, key) || strings.Contains(option.Description, key)) {
				return nil, nil, invalid
			}
			labels[option.Label] = true
			options = append(options, domain.Option{Label: option.Label, Description: option.Description})
		}
		questions = append(questions, domain.Question{
			ID:          strconv.Itoa(i),
			Header:      q.Header,
			Text:        q.Question,
			Options:     options,
			MultiSelect: q.MultiSelect,
			Custom:      true,
		})
	}
	return input.Questions, questions, nil
}

func questionAnswers(p approval, answers map[string][]string) (map[string]string, error) {
	reply := make(map[string]string, len(p.questions))
	for i, question := range p.questions {
		id := strconv.Itoa(i)
		var selected []string
		seen := make(map[string]bool)
		for _, raw := range answers[id] {
			value := strings.TrimSpace(raw)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			selected = append(selected, value)
		}
		if len(selected) == 0 || (!question.MultiSelect && len(selected) != 1) {
			return nil, errors.New("请为每个问题填写答案，单选题只能选择一项或填写自定义回答")
		}
		reply[question.Question] = strings.Join(selected, ", ")
	}
	return reply, nil
}

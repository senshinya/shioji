package codex

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

func asyncQuestions(item wire.Item) []domain.Question {
	var delivery string
	if json.Unmarshal(item.Fields["delivery"], &delivery) != nil || delivery != "async" {
		return nil
	}
	raw := item.Fields["questions"]
	if len(raw) > 16<<10 {
		return nil // The native readable text is still retained.
	}
	var source []struct {
		Title   string
		Options []string
	}
	if json.Unmarshal(raw, &source) != nil {
		return nil
	}
	var questions []domain.Question
	for index, q := range source {
		if strings.TrimSpace(q.Title) == "" {
			return nil
		}
		id, _ := json.Marshal([]any{"request_user_input_async", item.ID, index})
		if len(id) > 512 {
			return nil
		}
		question := domain.Question{ID: string(id), Text: q.Title, Custom: true}
		for _, option := range q.Options {
			question.Options = append(question.Options, domain.Option{Label: option})
		}
		questions = append(questions, question)
	}
	return questions
}

func asyncQuestionInput(text string, reply *domain.QuestionReply) string {
	if reply == nil {
		return text
	}
	title := reply.Title
	if len(title) > 512 {
		end := 512
		for !utf8.RuneStart(title[end]) {
			end--
		}
		title = title[:end]
	}
	body, _ := json.Marshal([]struct {
		ID       string `json:"questionItemId"`
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}{{reply.QuestionID, strings.NewReplacer("\n", " ", "\r", " ").Replace(title), text}})
	return "<send_user_message_question_reply>\n" + string(body) + "\n</send_user_message_question_reply>"
}

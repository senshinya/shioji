package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type questionHarness struct {
	memoryHarness
	replies chan harness.TurnInput
}

func (a questionHarness) Start(ctx context.Context, options harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	return &questionRuntime{memoryRuntime: r.(*memoryRuntime), replies: a.replies}, nil
}

type questionRuntime struct {
	*memoryRuntime
	replies chan harness.TurnInput
}

func (r *questionRuntime) Readiness() domain.Readiness {
	ready := r.memoryRuntime.Readiness()
	ready.Capabilities.Steer = true
	ready.Harnesses[0].Capabilities.Steer = true
	return ready
}
func (r *questionRuntime) Submit(ctx context.Context, s domain.Session, op string, input harness.TurnInput) (string, error) {
	if input.QuestionReply != nil {
		r.replies <- input
	}
	return r.memoryRuntime.Submit(ctx, s, op, input)
}
func (r *questionRuntime) Steer(_ context.Context, _ domain.Session, _ string, _ string, input harness.TurnInput) error {
	r.replies <- input
	return nil
}

func TestAsyncQuestionWorkerAnswersThroughActiveOrNextTurn(t *testing.T) {
	for _, mode := range []string{"steer", "submit"} {
		t.Run(mode, func(t *testing.T) {
			replies := make(chan harness.TurnInput, 3)
			s, w := memoryWorker(t, questionHarness{memoryHarness: memoryHarness{id: "fake", hold: mode == "steer"}, replies: replies})
			config := domain.RunConfig{Model: "fake-model", Permission: "limited"}
			first, err := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, HarnessID: "fake", Text: "Investigate", Config: config})
			if err != nil {
				t.Fatal(err)
			}
			if op := awaitReceipt(t, s, first.ID); op.State != "confirmed" {
				t.Fatal(op)
			}
			question := domain.Item{ID: "ask", Kind: "assistant", Final: true, TurnID: "turn/first",
				Text: "When?\n- After locking", Questions: []domain.Question{{ID: "opaque-question", Text: "When?", Custom: true}}}
			if _, err = s.store.Apply(first.SessionID, domain.Event{Type: "item.upsert", Item: &question}); err != nil {
				t.Fatal(err)
			}
			in := protocol.QuestionReplyInput{OperationID: "reply", SessionID: first.SessionID, ItemID: question.ID,
				QuestionID: question.Questions[0].ID, Text: "After locking", Config: config}
			send := func(in protocol.QuestionReplyInput) error {
				body, _ := json.Marshal(in)
				_, err := s.handle("question.reply", body)
				return err
			}
			if err := send(in); err != nil {
				t.Fatal(err)
			}
			if op := awaitReceipt(t, s, in.OperationID); op.State != "confirmed" || op.Kind != mode {
				t.Fatal(op)
			}
			select {
			case got := <-replies:
				if got.QuestionReply == nil || got.QuestionReply.Title != "When?" || got.QuestionReply.QuestionID != "opaque-question" || got.Text != "After locking" {
					t.Fatalf("wrong canonical answer: %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("answer did not reach the harness")
			}
			in.OperationID = "duplicate"
			if err := send(in); err == nil {
				t.Fatal("an already answered question accepted a second input")
			}
			d, err := s.store.Session(first.SessionID, 0, 100)
			if err != nil || len(d.Requests) != 0 {
				t.Fatalf("nonblocking question affected pending requests: %+v %v", d.Requests, err)
			}
			for _, item := range d.Items {
				if item.ReplyTo == question.Questions[0].ID && item.Status == "sent" {
					return
				}
			}
			t.Fatal("accepted answer did not persist its question association")
		})
	}
}

func TestAsyncQuestionRejectsSecretBeforeAcceptingASubmission(t *testing.T) {
	s, w := memoryWorker(t, questionHarness{memoryHarness: memoryHarness{id: "fake"}, replies: make(chan harness.TurnInput, 1)})
	config := domain.RunConfig{Model: "fake-model", Permission: "limited"}
	first, err := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, HarnessID: "fake", Text: "Investigate", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	item := domain.Item{ID: "secret-question", Kind: "assistant", Final: true,
		Questions: []domain.Question{{ID: "secret", Text: "Credential", Secret: true}}}
	if _, err := s.store.Apply(first.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
		t.Fatal(err)
	}
	_, err = s.replyToQuestion(protocol.QuestionReplyInput{OperationID: "answer", SessionID: first.SessionID, ItemID: item.ID,
		QuestionID: item.Questions[0].ID, Text: "private-value", Config: config})
	if err == nil || err.Error() != "请在 Agent 的原生界面回答保密问题" {
		t.Fatalf("unexpected reply result: %v", err)
	}
	if _, err := s.store.Operation("answer"); err == nil {
		t.Fatal("secret answer was accepted as an ordinary submission")
	}
}

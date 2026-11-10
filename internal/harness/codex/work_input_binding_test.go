package codex

import (
	"context"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"testing"
)

type workInputWriter struct{ write func([]byte) (int, error) }

func (w workInputWriter) Write(b []byte) (int, error) { return w.write(b) }
func (workInputWriter) Close() error                  { return nil }
func TestWorkInputAsyncCallsRejectChangedBindingAndMismatchedContent(t *testing.T) {
	for _, mode := range []string{"send-rebound", "receipt-rebound", "receipt-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("root", "s", false)
			n.workContinue = true
			workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t1","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"completed"}}}}}`)
			in := domain.WorkInput{OperationID: "op", SessionID: "s", WorkID: n.work.agents["child"].id, Mode: "continue", Text: "expected"}
			r := &Runtime{normalizer: n, protocol: &protocolSupport{methods: map[string]bool{"turn/start": true, "thread/turns/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
			r.stdin = workInputWriter{write: func(b []byte) (int, error) {
				var q wire.Message
				if err := json.Unmarshal(b, &q); err != nil {
					t.Fatal(err)
				}
				if mode != "receipt-mismatch" {
					workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t2","item":{"type":"collabAgentToolCall","id":"follow","tool":"sendInput","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"completed"}}}}}`)
				}
				result := json.RawMessage(`{"turn":{"id":"new-turn"}}`)
				if q.Method == "thread/turns/list" {
					text := "expected"
					if mode == "receipt-mismatch" {
						text = "DIFFERENT"
					}
					result, _ = json.Marshal(map[string]any{"data": []any{map[string]any{"id": "ct", "items": []any{map[string]any{"type": "userMessage", "id": "u", "clientId": "input:op", "content": []any{map[string]any{"type": "text", "text": text}}}}}}})
				}
				r.mu.Lock()
				ch := r.pending[string(q.ID)]
				r.mu.Unlock()
				ch <- wire.Message{Result: result}
				return len(b), nil
			}}
			session := domain.Session{ID: "s", ThreadID: "root"}
			if mode == "send-rebound" {
				err := r.SendWork(context.Background(), session, in)
				if !harness.IsUncertain(err) {
					t.Fatal("changed ownership confirmed an old work send", err)
				}
			} else {
				found, err := r.WorkInputReceived(context.Background(), session, in)
				if found || err == nil {
					t.Fatal("invalid source receipt confirmed a draft", mode, found, err)
				}
			}
		})
	}
}

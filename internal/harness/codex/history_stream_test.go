package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

// This subprocess only supplies the external protocol. The tests exercise the
// real runtime reader, RPC correlation, history consumers and normalization.
func TestHistoryStreamProcess(t *testing.T) {
	if os.Getenv("SHIOJI_HISTORY_STREAM_TEST") != "1" {
		return
	}
	count, _ := strconv.Atoi(os.Getenv("SHIOJI_HISTORY_STREAM_TURNS"))
	size, _ := strconv.Atoi(os.Getenv("SHIOJI_HISTORY_STREAM_BYTES"))
	output := "国😀\u2028\u2029" + strings.Repeat("x", size) + "tail"
	input := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for input.Scan() {
		var q wire.Message
		if json.Unmarshal(input.Bytes(), &q) != nil {
			os.Exit(2)
		}
		var result any
		switch q.Method {
		case "thread/read":
			result = map[string]any{"thread": map[string]any{"id": "thread", "cwd": "/device/project", "status": map[string]string{"type": "notLoaded"}}}
		case "thread/turns/list":
			turns := []any{}
			for i := 0; i < count; i++ {
				marker := fmt.Sprintf("input:receipt-%d", i)
				if i == count-1 {
					marker = "input:receipt"
				}
				turns = append(turns, map[string]any{"id": fmt.Sprintf("turn-%d", i), "status": "completed", "itemsView": "full", "items": []any{
					map[string]any{"id": "tool", "type": "commandExecution", "status": "completed", "command": "large output", "aggregatedOutput": output},
					map[string]any{"id": "user", "type": "userMessage", "clientId": marker, "content": []any{map[string]string{"type": "text", "text": "confirmed"}}},
					map[string]string{"id": "answer", "type": "agentMessage", "text": "after-large-output"},
				}})
			}
			result = map[string]any{"data": turns, "nextCursor": nil}
		default:
			os.Exit(3) // A history read must never resume, mutate or replay input.
		}
		raw, err := json.Marshal(result)
		if err != nil || encoder.Encode(wire.Message{ID: q.ID, Result: raw}) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func TestLargeHistoryFramesKeepRuntimeAndFollowingResponses(t *testing.T) {
	for _, shape := range []struct {
		name        string
		turns, size int
	}{{"ten-large-turns", 10, 2 << 20}, {"one-large-item", 1, 17 << 20}} {
		for _, consumer := range []string{"recovery", "import", "work-receipt"} {
			t.Run(shape.name+"/"+consumer, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestHistoryStreamProcess$")
				cmd.Env = append(os.Environ(), "SHIOJI_HISTORY_STREAM_TEST=1", "SHIOJI_HISTORY_STREAM_TURNS="+strconv.Itoa(shape.turns), "SHIOJI_HISTORY_STREAM_BYTES="+strconv.Itoa(shape.size))
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err = cmd.Start(); err != nil {
					t.Fatal(err)
				}
				n := NewNormalizer()
				n.Bind("root", "session", false)
				workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"parent","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["thread"],"agentsStates":{"thread":{"status":"completed"}}}}}`)
				r := &Runtime{cmd: cmd, stdin: stdin, normalizer: n, protocol: &protocolSupport{methods: map[string]bool{"thread/read": true, "thread/turns/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
				go r.read(stdout)
				defer r.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				s := domain.Session{ID: "session", ThreadID: "thread", Context: domain.LaunchContext{CWD: "/device/project"}}
				switch consumer {
				case "recovery":
					h, err := r.Inspect(ctx, s)
					if err != nil || len(h.Turns) != shape.turns || len(h.Items) != shape.turns*2 || h.Items[len(h.Items)-1].Text != "after-large-output" {
						t.Fatalf("large history recovery failed: turns=%d items=%d err=%v", len(h.Turns), len(h.Items), err)
					}
					if len(h.Items[0].Text) > 129<<10 || !strings.HasPrefix(h.Items[0].Text, "国😀\u2028\u2029") {
						t.Fatal("wire framing lost Unicode or bypassed the presentation bound")
					}
				case "import":
					h, err := r.ImportConversation(ctx, s.Context, s.ThreadID)
					if err != nil || h.Truncated || len(h.Turns) != shape.turns || len(h.Items) != shape.turns*3 || h.Items[len(h.Items)-1].Text != "after-large-output" {
						t.Fatalf("large history import failed: turns=%d items=%d err=%v", len(h.Turns), len(h.Items), err)
					}
				case "work-receipt":
					found, err := r.WorkInputReceived(ctx, domain.Session{ID: "session", ThreadID: "root"}, domain.WorkInput{SessionID: "session", WorkID: n.work.agents["thread"].id, OperationID: "receipt", Text: "confirmed"})
					if err != nil || !found {
						t.Fatalf("large history receipt failed: found=%v err=%v", found, err)
					}
				}
				var meta struct{ Thread struct{ ID string } }
				if err := r.call(ctx, "thread/read", map[string]string{"threadId": "thread"}, &meta); err != nil || meta.Thread.ID != "thread" || r.Stopped() {
					t.Fatal("large response stopped the runtime or lost the following response", err)
				}
			})
		}
	}
}

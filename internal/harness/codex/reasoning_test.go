package codex

import (
	"fmt"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func TestCodexNativeRetryUsesOneOwnedActivity(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"turn","status":"inProgress"}}}`)
	message := `{"method":"error","params":{"threadId":"thread","turnId":"turn","willRetry":true,"error":{"message":"provider detail not exposed"}}}`
	e := workFeed(t, n, message)
	if len(e) != 1 || e[0].Item == nil || e[0].Item.Final || e[0].Item.Name != "模型服务正在重试" {
		t.Fatalf("native retry was not shown: %+v", e)
	}
	if repeated := workFeed(t, n, message); len(repeated) != 0 {
		t.Fatalf("retry repeated the activity: %+v", repeated)
	}
	recovered := workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"completed"}}}`)
	if len(recovered) != 2 || recovered[0].Item == nil || recovered[0].Item.ID != e[0].Item.ID ||
		!recovered[0].Item.Final || recovered[1].Turn.Status != domain.ExecutionCompleted {
		t.Fatalf("retry row never settled: %+v", recovered)
	}
}

func TestCodexRetrySettlesWhenModelOutputReturns(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"turn","status":"inProgress"}}}`)
	message := `{"method":"error","params":{"threadId":"thread","turnId":"turn","willRetry":true,"error":{"message":"recoverable"}}}`
	first := workFeed(t, n, message)[0]
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"id":"answer","type":"agentMessage","text":""}}}`)
	events := workFeed(t, n, `{"method":"item/agentMessage/delta","params":{"threadId":"thread","turnId":"turn","itemId":"answer","delta":"reply"}}`)
	if len(events) != 2 || events[0].Item == nil || events[0].Item.ID != first.Item.ID ||
		events[0].Item.Name != "模型服务已恢复" || !events[0].Item.Final || events[1].Item.Text != "reply" {
		t.Fatalf("the recovered model still appears to be retrying: %+v", events)
	}
	if again := workFeed(t, n, message); len(again) != 1 || again[0].ActivityStart || again[0].Item.ID != first.Item.ID || again[0].Item.Final {
		t.Fatalf("the second retry did not update the original row: %+v", again)
	}
}

func TestCodexModelRerouteIsScopedToTheNotifyingThread(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	n.Bind("other", "other-session", false)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"turn","status":"inProgress"}}}`)
	packet := `{"method":"model/rerouted","params":{"threadId":"other","turnId":"turn","fromModel":"model-a","toModel":"model-b","reason":"internal"}}`
	if foreign := workFeed(t, n, packet); len(foreign) != 0 {
		t.Fatalf("foreign model event entered the transcript: %+v", foreign)
	}
	packet = strings.Replace(packet, "other", "thread", 1)
	events := workFeed(t, n, packet)
	if len(events) != 1 || events[0].Item == nil || events[0].Item.TurnID != "turn" ||
		events[0].Item.Name != "模型已切换 · model-a → model-b" || !events[0].Item.Final {
		t.Fatalf("native reroute was not shown: %+v", events)
	}
	if duplicate := workFeed(t, n, packet); len(duplicate) != 0 {
		t.Fatalf("reroute was duplicated: %+v", duplicate)
	}
}

func TestCodexReasoningSummaryStreamsAndFinalizesInPlace(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	start := workFeed(t, n, `{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"id":"thought","type":"reasoning","summary":[],"content":[]}}}`)[0]
	first := workFeed(t, n, `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":0,"delta":"先读"}}`)
	if len(first) != 1 || first[0].Type != "item.upsert" || first[0].Item == nil {
		t.Fatalf("first summary fragment did not update the timeline: %+v", first)
	}
	if event := first[0]; event.SessionID != "session" || event.ActivityStart || event.Item.ID != start.Item.ID ||
		event.Item.Presentation != "reasoning" || event.Item.Final || len(event.Item.Parts) != 1 || event.Item.Parts[0].Text != "先读" {
		t.Fatalf("first fragment must update the running summary in place: %+v", event.Item)
	}

	workFeed(t, n, `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":0,"delta":"文件。"}}`)
	if events := workFeed(t, n, `{"method":"item/reasoning/summaryPartAdded","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":1}}`); len(events) != 0 {
		t.Fatalf("a new paragraph without text should not change the timeline: %+v", events)
	}
	second := workFeed(t, n, `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":1,"delta":"再跑测试。"}}`)[0].Item
	if second.ID != start.Item.ID || len(second.Parts) != 2 || second.Parts[0].Text != "先读文件。" || second.Parts[1].Text != "再跑测试。" {
		t.Fatalf("summary paragraphs were merged or duplicated: %+v", second)
	}
	if first[0].Item.Parts[0].Text != "先读" {
		t.Fatal("later deltas rewrote a previously emitted snapshot")
	}

	final := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"thought","type":"reasoning","summary":["读了两个文件。","测试通过。"],"content":["raw text"]}}}`)[0].Item
	if !final.Final || final.ID != start.Item.ID || len(final.Parts) != 2 || final.Parts[0].Text != "读了两个文件。" || final.Parts[1].Text != "测试通过。" {
		t.Fatalf("native final summary did not replace the partial text: %+v", final)
	}
	if events := workFeed(t, n, `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":1,"delta":"late"}}`); len(events) != 0 {
		t.Fatalf("late summary fragment reopened the finalized item: %+v", events)
	}
}

func TestCodexChildReasoningSummaryStaysInTheChildTranscript(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	var work *domain.WorkActivity
	for _, event := range workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"parent-turn","item":{"id":"spawn","type":"subAgentActivity","agentThreadId":"child","agentPath":"/root/check","kind":"started"}}}`) {
		if event.Work != nil {
			work = event.Work
		}
	}
	if work == nil {
		t.Fatal("child work was not registered")
	}
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"child","turnId":"child-turn","item":{"id":"thought","type":"reasoning","summary":[],"content":[]}}}`)
	var item *domain.Item
	for _, event := range workFeed(t, n, `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"child","turnId":"child-turn","itemId":"thought","summaryIndex":0,"delta":"检查子任务。"}}`) {
		if event.Item != nil || event.Turn != nil {
			t.Fatalf("child summary entered the parent transcript: %+v", event)
		}
		if event.WorkItem != nil {
			if event.WorkID != work.ID || event.SessionID != "session" {
				t.Fatalf("summary went to unrelated work: %+v", event)
			}
			item = event.WorkItem
		}
	}
	if item == nil || item.TurnID != "parent-turn" || item.Final || len(item.Parts) != 1 || item.Parts[0].Text != "检查子任务。" {
		t.Fatalf("child transcript did not receive the running summary: %+v", item)
	}
}

func TestCodexReasoningSummaryStaysWithinItemBudget(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"id":"thought","type":"reasoning","summary":[]}}}`)
	for _, params := range []string{
		`"itemId":"unknown","summaryIndex":0,"delta":"unknown"`,
		`"itemId":"thought","summaryIndex":-1,"delta":"negative"`,
		`"itemId":"thought","summaryIndex":9223372036854775807,"delta":"too far"`,
	} {
		packet := `{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn",` + params + "}}"
		if events := workFeed(t, n, packet); len(events) != 0 {
			t.Fatalf("unowned or out-of-range summary fragment was accepted: %+v", events)
		}
	}
	body := strings.Repeat("字", 64<<10)
	events := workFeed(t, n, fmt.Sprintf(`{"method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread","turnId":"turn","itemId":"thought","summaryIndex":0,"delta":%q}}`, body))
	if len(events) != 1 || events[0].Item == nil || !events[0].Item.Truncated ||
		len(events[0].Item.Parts) != 1 || len(events[0].Item.Parts[0].Text) > 16<<10 {
		t.Fatal("oversized summary fragment was not bounded")
	}
}

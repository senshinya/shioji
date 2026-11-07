package codex

import (
	"encoding/json"
	"testing"
)

func TestContextUsageUsesLastRequestAndKeepsUnknownLimit(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	for _, limit := range []string{"200000", "null"} {
		events, _, err := n.Feed([]byte(`{"method":"thread/tokenUsage/updated","params":{"threadId":"thread","turnId":"turn","tokenUsage":{"total":{"inputTokens":900000,"outputTokens":10000,"totalTokens":910000},"last":{"inputTokens":24000,"outputTokens":1000,"totalTokens":25000},"modelContextWindow":` + limit + `}}}`))
		if err != nil || len(events) != 1 || events[0].Type != "context.usage" {
			t.Fatalf("missing independent context snapshot: %+v %v", events, err)
		}
		b, _ := json.Marshal(events[0])
		var e struct {
			ContextUsage struct {
				UsedTokens  *int64
				LimitTokens *int64
				TurnID      string
			}
		}
		json.Unmarshal(b, &e)
		if e.ContextUsage.UsedTokens == nil || *e.ContextUsage.UsedTokens != 25000 || e.ContextUsage.TurnID != "turn" {
			t.Fatalf("used cumulative billing counters for context: %s", b)
		}
		if limit == "null" && e.ContextUsage.LimitTokens != nil || limit != "null" && (e.ContextUsage.LimitTokens == nil || *e.ContextUsage.LimitTokens != 200000) {
			t.Fatalf("invented or lost model limit: %s", b)
		}
	}
}

func TestCompactionHasNeutralIdentityAndDoesNotClaimFailedCompletion(t *testing.T) {
	for _, tc := range []struct{ status, want string }{{"completed", "上下文已压缩"}, {"failed", "上下文压缩失败"}, {"interrupted", "上下文压缩已停止"}} {
		n := NewNormalizer()
		n.Bind("thread", "session", true)
		events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"compact","type":"contextCompaction","status":"` + tc.status + `"}}}`))
		if err != nil || len(events) != 1 || events[0].Item.Presentation != "compaction" || events[0].Item.Name != tc.want {
			t.Fatal("compaction status not normalized independently", tc, events, err)
		}
	}
}

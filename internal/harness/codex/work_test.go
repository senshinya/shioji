package codex

import (
	"encoding/json"
	"fmt"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestChildInitialUserMessageFillsActivityBriefWithoutDuplicatingInput(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprint(early), func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("root", "s", false)
			// A worker-accepted message is already persisted separately, even if
			// its notification arrives before the child's ownership is registered.
			workFeed(t, n, `{"method":"item/completed","params":{"threadId":"child","turnId":"ct","item":{"type":"userMessage","id":"accepted","clientId":"input:early-operation","content":[{"type":"text","text":"已接纳的追加消息"}]}}}`)
			user := `{"method":"item/completed","params":{"threadId":"child","turnId":"ct","item":{"type":"userMessage","id":"initial","content":[{"type":"text","text":"检查根节点"},{"type":"text","text":"与空子树"}]}}}`
			if early {
				workFeed(t, n, user)
			}
			es := workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"t","item":{"type":"subAgentActivity","id":"spawn","agentThreadId":"child","agentPath":"/root/check","kind":"started"}}}`)
			if !early {
				es = workFeed(t, n, user)
			}
			var brief string
			for _, e := range es {
				if e.Work != nil {
					brief = e.Work.Brief
				}
				if e.WorkItem != nil {
					t.Fatal("initial input duplicated in transcript", e)
				}
			}
			if brief != "检查根节点\n与空子树" {
				t.Fatalf("initial child prompt missing: %q", brief)
			}
			workFeed(t, n, `{"method":"item/completed","params":{"threadId":"child","turnId":"ct","item":{"type":"userMessage","id":"follow","clientId":"input:operation","content":[{"type":"text","text":"追加要求"}]}}}`)
			if n.work.works[n.work.agents["child"].id].Brief != brief {
				t.Fatal("followup replaced initial task")
			}
		})
	}
}

func TestSpawnPromptBackfillsEarlierActivityRegistration(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"t","item":{"type":"subAgentActivity","id":"activity","agentThreadId":"child","agentPath":"/root/check","kind":"started"}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"prompt":"完整初始任务"}}}`)
	for _, e := range es {
		if e.Work != nil && e.Work.Brief == "完整初始任务" {
			return
		}
	}
	t.Fatal("later spawn metadata did not fill the missing initial task")
}

func workFeed(t *testing.T, n *Normalizer, raw string) []domain.Event {
	t.Helper()
	e, _, err := n.Feed([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestSpawnCompletionDoesNotCompleteChildOrMixItsTranscript(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"root","turn":{"id":"parent-turn","status":"inProgress"}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"parent-turn","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"],"prompt":"Check rotation boundaries","model":"test-model","agentsStates":{"child":{"status":"running"}}}}}`)
	var work *domain.WorkActivity
	for _, e := range es {
		if e.Work != nil {
			work = e.Work
		}
	}
	if work == nil || work.Kind != "agent" || work.State != "running" {
		t.Fatal("spawn call completion was confused with child lifecycle", es)
	}
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"child","turnId":"child-turn","item":{"type":"agentMessage","id":"reply","text":"","status":"inProgress"}}}`)
	es = workFeed(t, n, `{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"child-turn","itemId":"reply","delta":"CHILD_ONLY"}}`)
	found := false
	for _, e := range es {
		if e.Type == "item.upsert" || e.Turn != nil {
			t.Fatal("child entered parent transcript/lifecycle", e)
		}
		found = found || e.WorkItem != nil && e.WorkItem.Text == "CHILD_ONLY"
	}
	if !found {
		b, _ := json.Marshal(es)
		t.Fatal("child output missing", string(b))
	}
	workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"parent-turn","status":"completed"}}}`)
	es = workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"child-turn","status":"completed","durationMs":35}}}`)
	found = false
	for _, e := range es {
		if e.Turn != nil {
			t.Fatal("child completion overwrote parent turn")
		}
		found = found || e.Work != nil && e.Work.State == "completed"
	}
	if !found {
		t.Fatal("child completion missing", es)
	}
}

func TestRuntimeInvalidationWithdrawsFinishedChildInputCapability(t *testing.T) {
	n := NewNormalizer()
	n.workContinue = true
	n.Bind("root", "s", false)
	workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"completed"}}}}}`)
	seen := false
	for _, e := range n.Invalidate() {
		if e.Work != nil && e.Work.Kind == "agent" {
			seen = true
			if e.Work.CanContinue || e.Work.CanSteer || e.Work.State != "completed" {
				t.Fatal("lost runtime kept control or erased finished state", e.Work)
			}
		}
	}
	if !seen {
		t.Fatal("finished child retained a stale send capability")
	}
}
func TestBackgroundCommandRequiresObservedConcurrentProgress(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	es := workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"t","item":{"type":"commandExecution","id":"cmd","command":"sleep 8","processId":"opaque-process","source":"unifiedExecStartup","status":"inProgress","startedAtMs":1794300153342}}}`)
	for _, e := range es {
		if e.Work != nil {
			t.Fatal("ordinary foreground command classified as background")
		}
	}
	workFeed(t, n, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"root","turnId":"t","itemId":"cmd","delta":"first\n"}}`)
	es = workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"t","status":"completed"}}}`)
	found := false
	for _, e := range es {
		found = found || e.Work != nil && e.Work.Kind == "background" && e.Work.State == "running" && e.Work.StartedAt != nil
	}
	if !found {
		t.Fatal("still-running command disappeared with parent completion", es)
	}
	es = workFeed(t, n, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"root","turnId":"t","itemId":"cmd","delta":"last\n"}}`)
	found = false
	for _, e := range es {
		found = found || e.WorkItem != nil && e.WorkItem.Text == "first\nlast\n"
	}
	if !found {
		t.Fatal("background append did not retain prior output", es)
	}
}

func TestNestedWorkAnchorsVisibleRootAndOldHistoryCannotReappear(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"child","turnId":"ct","item":{"type":"collabAgentToolCall","id":"nested","tool":"spawnAgent","receiverThreadIds":["grandchild"],"agentsStates":{"grandchild":{"status":"running"}}}}}`)
	found := false
	for _, e := range es {
		if e.Work != nil && e.Work.Reference == "grandchild" {
			found = true
			if e.Work.SourceItemID != "t/spawn" || e.Work.ParentID == "" {
				t.Errorf("nested work points outside visible root transcript: %+v", *e.Work)
			}
		}
	}
	if !found {
		t.Fatal("nested work missing")
	}
	delete(n.sessions, "root")
	n.Bind("new", "session", false)
	es = workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"ct","status":"completed"}}}`)
	if len(es) != 0 {
		t.Fatal("old child restored removed history", es)
	}
	for _, e := range n.Invalidate() {
		if e.Work != nil {
			t.Fatal("runtime invalidation resurrected old work", e)
		}
	}
}

func TestKnownChildFollowupBelongsToNewRootTurn(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t1","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"completed"}}}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t2","item":{"type":"collabAgentToolCall","id":"follow","tool":"sendInput","receiverThreadIds":["child"],"prompt":"Check again","agentsStates":{"child":{"status":"running"}}}}}`)
	found := false
	for _, e := range es {
		if e.Work != nil {
			found = true
			if e.Work.TurnID != "t2" || e.Work.SourceItemID != "t2/follow" || !e.Work.Active() {
				t.Fatal("followup retained old root ownership", *e.Work)
			}
		}
	}
	if !found {
		t.Fatal("known followup missing")
	}
	es = workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t2","item":{"type":"collabAgentToolCall","id":"foreign","tool":"sendInput","receiverThreadIds":["foreign"],"agentsStates":{"foreign":{"status":"running"}}}}}`)
	for _, e := range es {
		if e.Work != nil {
			t.Fatal("unowned child adopted through followup", e)
		}
	}
}

func TestChildStartedBeforeSpawnResultRemainsControllable(t *testing.T) {
	n := NewNormalizer()
	n.workControl = true
	n.Bind("root", "session", false)
	workFeed(t, n, `{"method":"turn/started","params":{"threadId":"child","turn":{"id":"ct","status":"inProgress","startedAt":100}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"pendingInit"}}}}}`)
	found := false
	for _, e := range es {
		if e.Work != nil {
			found = true
			if e.Work.State != "running" || !e.Work.CanStop || e.Work.StartedAt == nil {
				t.Fatal("early child lifecycle was lost", *e.Work)
			}
		}
	}
	if !found {
		t.Fatal("no child")
	}
}

func TestLongWorkBriefFitsNeutralMetadataLimit(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	raw, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "root", "turnId": "t", "item": map[string]any{"type": "collabAgentToolCall", "id": "spawn", "tool": "spawnAgent", "receiverThreadIds": []string{"child"}, "prompt": strings.Repeat("界", 4000)}}})
	es := workFeed(t, n, string(raw))
	for _, e := range es {
		if e.Work != nil && len(e.Work.Brief) > 8192 {
			t.Fatal("adapter exceeded accepted metadata byte bound", len(e.Work.Brief))
		}
	}
}

func TestBackgroundInterruptionUsesNeutralWorkState(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"t","item":{"type":"commandExecution","id":"cmd","command":"sleep 8","processId":"p","status":"inProgress"}}}`)
	workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"t","status":"completed"}}}`)
	es := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"commandExecution","id":"cmd","command":"sleep 8","processId":"p","status":"interrupted"}}}`)
	found := false
	for _, e := range es {
		if e.Work != nil {
			found = true
			if e.Work.State != "stopped" {
				t.Fatal("concrete command state escaped adapter", e.Work.State)
			}
		}
	}
	if !found {
		t.Fatal("background end missing")
	}
}

func TestTerminalTurnPublishesBackgroundOwnershipBeforeCompletion(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"root","turnId":"t","item":{"type":"commandExecution","id":"cmd","command":"sleep 8","processId":"p","status":"inProgress"}}}`)
	es := workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"t","status":"completed"}}}`)
	owned := false
	for _, e := range es {
		owned = owned || e.Work != nil && e.Work.Kind == "background" && e.Work.Active()
		if e.Turn != nil && e.Turn.Status == "completed" && !owned {
			t.Fatal("parent completion released ownership before background was persisted")
		}
	}
}

func TestChildCompletionPublishesItsBackgroundOwnershipFirst(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	spawned := workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
	active := map[string]bool{}
	for _, e := range spawned {
		if e.Work != nil {
			active[e.Work.ID] = e.Work.Active()
		}
	}
	workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"t","status":"completed"}}}`)
	workFeed(t, n, `{"method":"item/started","params":{"threadId":"child","turnId":"ct","item":{"type":"commandExecution","id":"cmd","command":"sleep 8","processId":"p","status":"inProgress"}}}`)
	es := workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"ct","status":"completed"}}}`)
	for _, e := range es {
		if e.Work == nil {
			continue
		}
		active[e.Work.ID] = e.Work.Active()
		owned := false
		for _, running := range active {
			owned = owned || running
		}
		if !owned {
			t.Fatal("last child completion released ownership before its background command")
		}
	}
}

func TestRuntimeLossGuardsSessionBeforeInvalidatingLastChild(t *testing.T) {
	n := NewNormalizer()
	n.Bind("root", "s", false)
	workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
	guarded := false
	for _, e := range n.Invalidate() {
		guarded = guarded || e.Type == "runtime.lost"
		if e.Work != nil && e.Work.State == "unknown" && !guarded {
			t.Fatal("unknown child released ownership before managed execution guard")
		}
	}
}

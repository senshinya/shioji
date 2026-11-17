package worker

import (
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"testing"
	"time"
)

func TestNativeBackgroundProtocolReachesIdleGC(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "audit-bg", "")
	sid, thread, turn := d.Session.ID, d.Session.ThreadID, d.Turns[0].ID
	n := codex.NewNormalizer()
	n.Bind(thread, sid, false)
	raws := []string{
		fmt.Sprintf(`{"method":"item/started","params":{"threadId":%q,"turnId":%q,"item":{"type":"commandExecution","id":"native-bg","command":"sleep 999999","processId":"process-native-bg","status":"inProgress"}}}`, thread, turn),
		fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":%q,"status":"completed"}}}`, thread, turn),
	}
	var bg domain.WorkActivity
	for _, raw := range raws {
		events, _, e := n.Feed([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		for _, ev := range events {
			if ev.Work != nil {
				bg = *ev.Work
			}
			if _, e = s.store.Apply(sid, ev); e != nil {
				t.Fatal(e)
			}
		}
	}

	state, e := s.store.RuntimeGCIdle(sid)
	if e != nil {
		t.Fatal(e)
	}
	if bg.State != "waiting" || !state.Idle || !state.Background {
		t.Fatal("actual adapter events did not reach waiting background GC", bg, state)
	}
	old := s.currentRuntime(sid)
	now := time.Now()
	sweepIdle(t, s, now)
	sweepIdle(t, s, now.Add(23*time.Hour))
	if old.Stopped() {
		t.Fatal("background process lost its protection early")
	}
	sweepIdle(t, s, now.Add(24*time.Hour))
	if !old.Stopped() || s.currentRuntime(sid) != nil {
		t.Fatal("native background never retired at cap")
	}
	after, e := s.store.Session(sid, 0, 100)
	if e != nil || after.Session.ThreadID != thread {
		t.Fatal("native background retirement lost original history", e)
	}
}

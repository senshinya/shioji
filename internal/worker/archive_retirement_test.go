package worker

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestArchiveRetiresImmediatelyAndRestoreDoesNotLaunch(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	before := submitIdleFixture(t, s, w, "archive-runtime", "")
	id := before.Session.ID
	r := s.currentRuntime(id)
	raw, _ := json.Marshal(map[string]any{"sessionId": id, "revision": before.Session.Revision, "archived": true})
	out, err := s.handle("session.archive", raw)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("archive did not retire its runtime immediately")
	}
	archived := out.(domain.Session)
	raw, _ = json.Marshal(map[string]any{"sessionId": id, "revision": archived.Revision, "archived": false})
	if _, err = s.handle("session.archive", raw); err != nil {
		t.Fatal(err)
	}
	after, err := s.store.Session(id, 0, 100)
	if err != nil || len(after.Items) != len(before.Items) {
		t.Fatal("archive/restore changed conversation", err)
	}
	if current := s.currentRuntime(id); current != nil && !current.Stopped() {
		t.Fatal("restore relaunched execution")
	}
}

package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestArchiveRetiresWhileOwnedWorktreeBackupWaits(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	other := submitIdleFixture(t, s, w, "audit-independent", "")
	old := s.currentRuntime(other.Session.ID)
	if e := os.WriteFile(filepath.Join(d.Session.Context.CWD, "new-audit.txt"), []byte("preserved"), 0600); e != nil {
		t.Fatal(e)
	}
	gate, release := holdLifecycleGit(t, "add")
	arch := func(id string) {
		latest, e := s.store.Session(id, 0, 1)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(protocol.SessionChangeInput{SessionID: id, Revision: latest.Session.Revision, Archived: true})
		if _, e = s.handle("session.archive", b); e != nil {
			t.Fatal(e)
		}
	}
	arch(d.Session.ID)
	awaitLifecycleGit(t, gate)
	arch(other.Session.ID)
	for until := time.Now().Add(time.Second); !old.Stopped() && time.Now().Before(until); {
		time.Sleep(time.Millisecond)
	}
	retained := !old.Stopped()
	release()
	for until := time.Now().Add(5 * time.Second); !old.Stopped() && time.Now().Before(until); {
		time.Sleep(time.Millisecond)
	}
	t.Logf("OBSERVATION independentArchiveAccepted=true independentRuntimeRetainedDuringWorktreeGit=%v retiredAfterGitReply=%v", retained, old.Stopped())
	if retained || !old.Stopped() {
		t.Fatal("another checkout backup delayed archived runtime retirement")
	}
}

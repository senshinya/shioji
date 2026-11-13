package tailnet

import (
	"fmt"
	"testing"
	"time"
)

func TestLongLivedNodeBoundsRoutesAndReclaimsIdleListeners(t *testing.T) {
	n := New(t.TempDir(), nil)
	defer n.Close()
	for i := 0; i < 128; i++ {
		if _, err := n.Route(fmt.Sprintf("peer-%d", i), "100.64.0.1", 22); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := n.Route("overflow", "100.64.0.1", 22); err == nil {
		t.Fatal("long-running service has unbounded route listeners")
	}
	for _, b := range n.bridges {
		b.mu.Lock()
		b.lastUse = time.Now().Add(-3 * time.Minute)
		b.mu.Unlock()
	}
	if _, err := n.Route("after-idle", "100.64.0.1", 22); err != nil {
		t.Fatal("idle routes were not reclaimed", err)
	}
	if len(n.bridges) != 1 {
		t.Fatal("idle listeners retained", len(n.bridges))
	}
}

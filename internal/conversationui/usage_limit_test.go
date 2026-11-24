package conversationui

import (
	"testing"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestUsageLimitNoticeDoesNotReplayEntranceOnSessionMount(t *testing.T) {
	turn := domain.Turn{ID: "existing-limit", Status: domain.ExecutionFailed, UsageLimit: &domain.UsageLimit{}}
	var height float32
	tester := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			ComposerNotice(c.Key("usage-limit"), turn, "", n.Hex("#f1f1f3"), n.Hex("#18181b"), n.Hex("#66666e"), 1, 76, 8, 28, nil)
			n.Box(c.Key("below")).FillWidth().Height(10).DrawOver(func(_ *n.Painter, r n.Rect) { height = r.Y })
		})
	}, 420, 200)

	// An already-present usage limit on initial mount (e.g. switching sessions) must appear
	// settled at its full height immediately instead of replaying a 220ms entrance + 420ms tide.
	if height < 48 {
		t.Fatalf("existing usage limit started collapsed at height %g instead of settled", height)
	}
	if tester.FramePending() {
		t.Fatal("existing usage limit replayed animation frames on mount")
	}
}

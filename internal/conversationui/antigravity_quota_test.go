package conversationui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

func TestAntigravityQuotaHistoryRequiresFinalProviderWarning(t *testing.T) {
	const warning = "⚠ Individual quota reached. Please upgrade your subscription to increase your limits."
	completed := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		name, harness, kind, text, want string
		final                           bool
		completed                       *time.Time
	}{
		{"no reset", "antigravity", "assistant", warning, "重置时间暂未知", true, &completed},
		{"invalid reset", "antigravity", "assistant", warning + " Resets in later.", "重置时间暂未知", true, &completed},
		{"no completion date", "antigravity", "assistant", warning + " Resets in 3h50m52s.\r\n", "重置时间暂未知", true, nil},
		{"zero delay", "antigravity", "assistant", warning + " Resets in 0s.", "预计重置时间已到", true, &completed},
		{"ordinary inline text", "antigravity", "assistant", "Example: " + warning, "", true, &completed},
		{"warning quoted before prose", "antigravity", "assistant", warning + "\nMore details.", "", true, &completed},
		{"other harness", "codex", "assistant", warning, "", true, &completed},
		{"user message", "antigravity", "user", warning, "", true, &completed},
		{"streaming text", "antigravity", "assistant", warning, "", false, &completed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := domain.Item{ID: "reply", TurnID: "turn", Kind: tc.kind, Text: tc.text, Final: tc.final}
			body, _, found := SplitAntigravityQuotaWarning(tc.harness, item)
			detail := domain.SessionDetail{
				Session: domain.Session{HarnessID: tc.harness},
				Turns:   []domain.Turn{{ID: "turn", Status: domain.ExecutionCompleted, CompletedAt: tc.completed}},
				Items:   []domain.Item{item},
			}
			turn, source := ComposerWarning(detail, domain.Turn{})
			if tc.want == "" {
				if found || body != tc.text || turn.UsageLimit != nil || source != "" {
					t.Fatalf("ordinary content changed: %q", body)
				}
				return
			}
			if !found || body != "" || turn.UsageLimit == nil || source != item.ID {
				t.Fatalf("standalone warning was not recognized: %q", body)
			}
			tester := n.NewTester(func(c *n.Context) {
				ComposerNotice(c, turn, source, n.Hex("#f1f1f3"), n.Hex("#18181b"), n.Hex("#66666e"), 1, 52, 0, 44, nil)
			}, 350, 100)
			if !tester.HasText(tc.want) || tester.FramePending() {
				t.Fatalf("expected %q without a pending countdown", tc.want)
			}
		})
	}
}

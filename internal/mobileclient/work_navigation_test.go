package mobileclient

import (
	"testing"

	"shioji/internal/domain"
)

func TestWorkSourceTargetsVisibleConversationFromSameTurn(t *testing.T) {
	work := domain.WorkActivity{ID: "child", TurnID: "turn", SourceItemID: "source"}
	detail := domain.SessionDetail{Works: []domain.WorkActivity{work}, Items: []domain.Item{
		{ID: "previous", Kind: "user", TurnID: "previous"},
		{ID: "prompt", Kind: "user", TurnID: "turn"},
		{ID: "source", Kind: "tool", TurnID: "turn"},
		{ID: "private", Kind: "assistant", WorkID: "child", TurnID: "turn", Text: "Child only"},
		{ID: "reply", Kind: "assistant", TurnID: "turn", Text: "Parent reply"},
		{ID: "following", Kind: "user", TurnID: "following"},
	}}
	if got := WorkSourceAnchor(detail, work); got != "prompt" {
		t.Fatalf("want visible source context, got %q", got)
	}
	detail.Items = append(detail.Items[:1], detail.Items[2:]...)
	if got := WorkSourceAnchor(detail, work); got != "reply" {
		t.Fatalf("want next visible item within the same turn, got %q", got)
	}
	detail.Items[3].TurnID = "different"
	if got := WorkSourceAnchor(detail, work); got != "" {
		t.Fatalf("must never jump to unrelated turns, got %q", got)
	}
}

func TestBackgroundWorkOutputCopiesExactlyWhatTheTerminalDisplays(t *testing.T) {
	items := []domain.Item{{Text: "first\n"}, {Text: ""}, {Text: "  second"}}
	if got := BackgroundWorkOutput(items, "duplicate summary"); got != "first\n\n  second" {
		t.Fatalf("lost output or added unrelated text: %q", got)
	}
	if got := BackgroundWorkOutput(nil, "legacy tail"); got != "legacy tail" {
		t.Fatalf("legacy tail lost: %q", got)
	}
}

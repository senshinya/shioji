package conversationui

import (
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestReasoningStreamingHoldsTrailingNewlinesUntilMeaningfulContent(t *testing.T) {
	chunks := []struct {
		raw  string
		want string
	}{
		{"   ", ""},
		{"   \n\n", ""},
		{"   \n\n\t ", ""},
		{"First paragraph.", "First paragraph."},
		{"First paragraph.\n", "First paragraph."},
		{"First paragraph.\n\n\n", "First paragraph."},
		{"First paragraph.\n\n\n  \t\r\n", "First paragraph."},
		{"First paragraph.\n\n\nSecond paragraph.\n\n\n", "First paragraph.\n\n\nSecond paragraph."},
		{"First paragraph.\n\n\nSecond paragraph.\n\n\n\n", "First paragraph.\n\n\nSecond paragraph."},
	}
	for _, tc := range chunks {
		item := domain.Item{Kind: "activity", Presentation: "reasoning", Text: tc.raw}
		if got := ReasoningForDisplay(item).Text; got != tc.want {
			t.Errorf("ReasoningForDisplay(%q).Text = %q, want %q", tc.raw, got, tc.want)
		}
		if got := ReasoningSummaryText(item); got != tc.want {
			t.Errorf("ReasoningSummaryText(%q) = %q, want %q", tc.raw, got, tc.want)
		}
		partItem := domain.Item{Kind: "activity", Presentation: "reasoning", Parts: []domain.ContentPart{{Kind: "text", Text: tc.raw}}}
		displayParts := ReasoningForDisplay(partItem).Parts
		gotPart := ""
		if len(displayParts) > 0 {
			gotPart = displayParts[0].Text
		}
		if gotPart != tc.want {
			t.Errorf("ReasoningForDisplay(part %q) = %q, want %q", tc.raw, gotPart, tc.want)
		}
		if got := ReasoningSummaryText(partItem); got != tc.want {
			t.Errorf("ReasoningSummaryText(part %q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// Verify strict prefix monotonicity across arbitrary streaming increments,
	// including leading spaces, same-line trailing spaces, and multi-newlines.
	increments := []string{"  ", "\n", "Alpha", "  ", "\n", "   ", "\n\n", "Beta ", "gamma", "\r\n", "\n"}
	var cumulative, prev string
	for _, delta := range increments {
		cumulative += delta
		got := ReasoningForDisplay(domain.Item{Kind: "activity", Presentation: "reasoning", Text: cumulative}).Text
		if !strings.HasPrefix(got, prev) {
			t.Fatalf("display text shrank or broke prefix monotonicity: prev=%q next=%q (raw=%q)", prev, got, cumulative)
		}
		prev = got
	}
	if prev != "  \nAlpha  \n   \n\nBeta gamma" {
		t.Fatalf("final streamed text = %q, want trailing newlines dropped", prev)
	}
}

func TestReasoningTextStreamRevealsProgressivelyWithoutTrailingNewlineFlash(t *testing.T) {
	now := time.Unix(100, 0)
	var stream ReasoningTextStream

	// Fresh live burst reveals character-by-character rather than all at once.
	burst := strings.Repeat("Checking prime candidates carefully. ", 10)
	first := stream.Advance(now, burst, true, true, false)
	if len(first.Visible) == 0 || len(first.Visible) >= len(burst) || !first.Animating {
		t.Fatalf("fresh live burst should start streaming progressively, got len=%d/%d animating=%t", len(first.Visible), len(burst), first.Animating)
	}
	// Multiple layout passes at the same frame timestamp must not fast-forward the reveal.
	for i := 0; i < 10; i++ {
		again := stream.Advance(now, burst, true, true, false)
		if again.Visible != first.Visible {
			t.Fatalf("same-timestamp rebuild #%d advanced reveal: %d -> %d", i, len(first.Visible), len(again.Visible))
		}
	}
	if len(first.Fades) == 0 {
		t.Fatalf("expected fading tail spans during reveal: %+v", first)
	}
	// Advance frame by frame until settled.
	settled := false
	for i := 0; i < 200; i++ {
		now = now.Add(16 * time.Millisecond)
		frame := stream.Advance(now, burst, true, true, false)
		if !strings.HasPrefix(burst, frame.Visible) {
			t.Fatalf("visible text diverged from source: %q", frame.Visible)
		}
		if frame.Visible == burst && !frame.Animating {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatalf("burst did not settle within 3.2s")
	}

	// Appending a second paragraph separated by multiple newlines must never
	// expose a prefix ending in a line break on any intermediate frame.
	second := burst + "\n\n\nNext stage verifies every remaining case."
	for i := 0; i < 200; i++ {
		now = now.Add(16 * time.Millisecond)
		frame := stream.Advance(now, second, true, true, false)
		if strings.HasSuffix(frame.Visible, "\n") || strings.HasSuffix(frame.Visible, "\r") {
			t.Fatalf("intermediate frame %d exposed trailing newline: %q", i, frame.Visible[max(0, len(frame.Visible)-12):])
		}
		if frame.Visible == second && !frame.Animating {
			break
		}
	}
	if final := stream.Advance(now, second, true, true, false); final.Visible != second {
		t.Fatalf("second paragraph did not complete: got %d/%d bytes", len(final.Visible), len(second))
	}

	// Non-live or reduced motion returns full text immediately with no animation.
	var hydrated ReasoningTextStream
	snap := hydrated.Advance(now, burst, true, false, false)
	if snap.Visible != burst || snap.Animating {
		t.Fatalf("hydrated stream should not replay existing text: len=%d/%d animating=%t", len(snap.Visible), len(burst), snap.Animating)
	}
	// Subsequent live chunk after hydration still streams progressively.
	next := hydrated.Advance(now.Add(16*time.Millisecond), second, true, false, false)
	if len(next.Visible) <= len(burst) || len(next.Visible) >= len(second) || !next.Animating {
		t.Fatalf("new chunk after hydration should stream progressively: len=%d (burst=%d, second=%d) animating=%t", len(next.Visible), len(burst), len(second), next.Animating)
	}

	var reduced ReasoningTextStream
	if got := reduced.Advance(now, second, true, true, true); got.Visible != second || got.Animating {
		t.Fatalf("reduceMotion should reveal immediately: len=%d animating=%t", len(got.Visible), got.Animating)
	}
}

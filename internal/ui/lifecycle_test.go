package ui

import (
	"testing"
	"time"
)

func TestReplyPhaseTransitions(t *testing.T) {
	for live := 0; live < 2; live++ {
		for fresh := 0; fresh < 2; fresh++ {
			for sequence := 0; sequence < 64; sequence++ {
				s := replyStream{phase: replyPhaseFor(live == 1, fresh == 1)}
				wantLive, wantFresh := live == 1, fresh == 1
				for step := 0; step < 6; step++ {
					if sequence>>step&1 == 1 {
						s.mount()
						wantFresh = false
					} else {
						prefix := step%2 == 0
						s.replace(prefix)
						wantLive = prefix
					}
					if s.live() != wantLive || s.fresh() != wantFresh {
						t.Fatalf("%d/%d sequence=%d step=%d phase=%v", live, fresh, sequence, step, s.phase)
					}
				}
			}
		}
	}
}

func TestPreviewPresenceTransitions(t *testing.T) {
	for _, closeBeforeMeasure := range []bool{false, true} {
		p := attachmentPreview{presence: previewOpening}
		if !p.open() || !p.visible() || p.motionReady() {
			t.Fatal("initial presence", p.presence)
		}
		if closeBeforeMeasure {
			p.close()
			if p.open() || !p.visible() || p.motionReady() {
				t.Fatal("early close", p.presence)
			}
		}
		p.measured()
		if !p.motionReady() || !p.visible() {
			t.Fatal("measurement", p.presence)
		}
		p.close()
		if p.open() || !p.visible() {
			t.Fatal("closing", p.presence)
		}
		p.presence = previewHidden
		if p.open() || p.visible() {
			t.Fatal("hidden", p.presence)
		}
	}
}

func TestRecoveryCheckDelayDoesNotReuseEarlierAttempt(t *testing.T) {
	var state recoveryState
	start := time.Unix(100, 0)
	state.observeCheck(true, start)
	if state.check != recoveryCheckWaiting {
		t.Fatal("checking must wait before appearing")
	}
	state.observeCheck(true, start.Add(299*time.Millisecond))
	if state.check != recoveryCheckWaiting {
		t.Fatal("appeared early")
	}
	state.observeCheck(true, start.Add(300*time.Millisecond))
	if state.check != recoveryCheckVisible {
		t.Fatal("check never appeared")
	}
	state.observeCheck(false, start.Add(time.Second))
	if state.check != recoveryCheckIdle {
		t.Fatal("resolved check retained its phase")
	}
	state.observeCheck(true, start.Add(2*time.Second))
	if state.check != recoveryCheckWaiting || !state.checkStarted.Equal(start.Add(2*time.Second)) {
		t.Fatal("new attempt reused old timer")
	}
}

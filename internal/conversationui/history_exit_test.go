package conversationui

import (
	"testing"
	"time"
)

func TestHistoryExitKeepsLayoutUntilParticlesFinish(t *testing.T) {
	start := time.Unix(100, 0)
	if phase := HistoryExitAt(start.Add(500*time.Millisecond), start, true, false); phase.Opacity != 1 || phase.Shatter <= 0 || phase.Shatter >= 1 || phase.Height != 1 || phase.Dispatch {
		t.Fatalf("particle exit lost its occupied space: %+v", phase)
	}
	if phase := HistoryExitAt(start.Add(1250*time.Millisecond), start, true, false); phase.Opacity != 0 || phase.Height <= 0 || phase.Height >= 1 || phase.Dispatch {
		t.Fatalf("height must shrink after the particles exit: %+v", phase)
	}
	if phase := HistoryExitAt(start.Add(1350*time.Millisecond), start, true, false); phase.Height != 0 || !phase.Dispatch {
		t.Fatalf("motion didn't release the submission: %+v", phase)
	}
	if phase := HistoryExitAt(start, start, true, true); phase.Height != 0 || !phase.Dispatch {
		t.Fatalf("reduce motion shouldn't delay submission: %+v", phase)
	}
}

func TestForkFadesWithoutMovingItsSource(t *testing.T) {
	start := time.Unix(100, 0)
	if phase := HistoryExitAt(start.Add(110*time.Millisecond), start, false, false); phase.Opacity <= 0 || phase.Opacity >= 1 || phase.Height != 1 || phase.Dispatch {
		t.Fatalf("fork should fade in place: %+v", phase)
	}
	if phase := HistoryExitAt(start.Add(220*time.Millisecond), start, false, false); phase.Opacity != 0 || phase.Height != 1 || !phase.Dispatch {
		t.Fatalf("fork didn't release navigation after fading: %+v", phase)
	}
}

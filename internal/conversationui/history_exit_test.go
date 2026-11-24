package conversationui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
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

func TestHistoryShatterFitsRespectsLayerCeiling(t *testing.T) {
	if !HistoryShatterFits(700, 600, 2) {
		t.Fatal("normal desktop message must fit effect layer")
	}
	if !HistoryShatterFits(390, 2000, 3) {
		t.Fatal("2000 DIP iOS message must fit effect layer at 3x")
	}
	if HistoryShatterFits(700, 4100, 2) {
		t.Fatal("4100 DIP message exceeds 8192 px ceiling at 2x")
	}
	if HistoryShatterFits(390, 2750, 3) {
		t.Fatal("2750 DIP message exceeds 8192 px ceiling at 3x")
	}
	if HistoryShatterFits(0, 100, 2) || HistoryShatterFits(100, 0, 2) {
		t.Fatal("empty bounds must not report fitting")
	}
}

func TestShatterGrainSizeStaysFineAndBounded(t *testing.T) {
	if got := shatterGrainSize(400, 80); got != shatterBaseGrain {
		t.Fatalf("short bubble should keep base grain %.1f, got %.2f", shatterBaseGrain, got)
	}
	tall := shatterGrainSize(700, 600)
	cells := (700 / tall) * (600 / tall)
	if cells > shatterMaxGrains*1.05 {
		t.Fatalf("tall message grid exceeded budget: %.0f cells (grain %.2f)", cells, tall)
	}
	if (Shatter{}).EffectMargin() != (n.Point{}) {
		t.Fatalf("shatter should not pad offscreen layer with empty margin: %+v", (Shatter{}).EffectMargin())
	}
}

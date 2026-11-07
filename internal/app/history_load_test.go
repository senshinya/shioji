package app

import "testing"

func TestHistoryLoadTransitionsMatchVisitFlags(t *testing.T) {
	for sequence := 0; sequence < 4096; sequence++ {
		state := historyUnloaded
		loaded, opening := false, false
		for step := 0; step < 6; step++ {
			switch sequence >> (step * 2) & 3 {
			case 0:
				state = historyOpening
				loaded, opening = false, true
			case 1:
				state = state.failed()
				loaded = true
			case 2:
				state = historyLoaded
				loaded, opening = true, false
			case 3:
				state = state.invalidated()
				loaded = false
			}
			if state.loaded() != loaded || state.opening() != opening {
				t.Fatalf("sequence=%d step=%d state=%v want loaded=%v opening=%v", sequence, step, state, loaded, opening)
			}
		}
	}
}

package app

import (
	"path/filepath"
	"testing"
)

func TestToolDisclosureDefaultPreferenceAndExplicitChoiceSurviveRestart(t *testing.T) {
	m := NewModel()
	key := "tool-group:s/latest"
	if m.ToolDisclosure(key).Closed() {
		t.Fatal("legacy default changed")
	}
	m.Preferences.CollapseLatestTools = true
	if !m.ToolDisclosure(key).Closed() {
		t.Fatal("latest tools ignored default folding preference")
	}
	m.SetToolDisclosure(key, m.ToolDisclosure(key).Toggle())
	if m.ToolDisclosure(key).Closed() {
		t.Fatal("preference overrides explicit expansion")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if !restored.Preferences.CollapseLatestTools || restored.ToolDisclosure(key).Closed() || !restored.ToolDisclosure("tool-group:s/new").Closed() {
		t.Fatal("default or manual choice lost after restart")
	}
}

func TestToolDisclosurePreservesProseBoundaryAndUserChoice(t *testing.T) {
	for bits := 0; bits < 4; bits++ {
		for sequence := 0; sequence < 256; sequence++ {
			m := NewModel()
			key := "tool-group:s/tool"
			closed, prose := bits&1 != 0, bits&2 != 0
			m.Collapsed[key], m.Collapsed[key+":prose"] = closed, prose
			for step := 0; step < 4; step++ {
				group := m.ToolDisclosure(key)
				switch sequence >> (step * 2) & 3 {
				case 0:
					group = group.Toggle()
					closed = !closed
				case 1:
					group = group.OpenHistory()
					closed = true
				case 2:
					group = group.ObserveProse()
					if !prose {
						closed = true
						prose = true
					}
				case 3:
					group = ToolOpenedAfterProse
					closed, prose = false, true
				}
				m.SetToolDisclosure(key, group)
				if m.Collapsed[key] != closed || m.Collapsed[key+":prose"] != prose || group.Closed() != closed || group.SawProse() != prose {
					t.Fatalf("bits=%d sequence=%d step=%d group=%v", bits, sequence, step, group)
				}
			}
		}
	}
}

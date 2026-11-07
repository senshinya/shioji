package app

import "testing"

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

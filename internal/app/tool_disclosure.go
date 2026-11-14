package app

// Explicit user disclosure is independent of observing the first later prose.
// Persist through the existing layout keys so older client state stays readable.
type ToolDisclosure uint8

const (
	ToolOpenedBeforeProse ToolDisclosure = iota
	ToolClosedBeforeProse
	ToolOpenedAfterProse
	ToolClosedAfterProse
)

func (s ToolDisclosure) Closed() bool { return s == ToolClosedBeforeProse || s == ToolClosedAfterProse }
func (s ToolDisclosure) SawProse() bool {
	return s == ToolOpenedAfterProse || s == ToolClosedAfterProse
}
func (s ToolDisclosure) Toggle() ToolDisclosure {
	if s.Closed() {
		if s.SawProse() {
			return ToolOpenedAfterProse
		}
		return ToolOpenedBeforeProse
	}
	return s.OpenHistory()
}
func (s ToolDisclosure) OpenHistory() ToolDisclosure {
	if s.SawProse() {
		return ToolClosedAfterProse
	}
	return ToolClosedBeforeProse
}
func (s ToolDisclosure) ObserveProse() ToolDisclosure {
	if s.SawProse() {
		return s
	}
	return ToolClosedAfterProse
}
func (m *Model) ToolDisclosure(key string) ToolDisclosure {
	state := ToolOpenedBeforeProse
	if m.Collapsed[key+":prose"] {
		state = ToolOpenedAfterProse
	}
	closed, chosen := m.Collapsed[key]
	if closed || !chosen && m.Preferences.CollapseLatestTools {
		state = state.OpenHistory()
	}
	return state
}
func (m *Model) SetToolDisclosure(key string, state ToolDisclosure) {
	// The prose boundary is written only when observed; ordinary toggles
	// must not add a false marker to persisted layout state.
	m.Collapsed[key] = state.Closed()
	if state.SawProse() || m.Collapsed[key+":prose"] {
		m.Collapsed[key+":prose"] = state.SawProse()
	}
}

package app

import "shioji/internal/domain"

// A session draft holds only the choices made in this composer's menus.
// Its other fields follow the owning worker's latest session configuration.
type sessionConfigDraft struct {
	Model      *string `json:"model,omitempty"`
	Effort     *string `json:"effort,omitempty"`
	Permission *string `json:"permission,omitempty"`
	Plan       *bool   `json:"plan,omitempty"`
	Fast       *bool   `json:"fast,omitempty"`
}

func (m *Model) acceptedSessionConfig(id string) domain.RunConfig {
	for _, s := range m.Snapshot.Sessions {
		if s.ID == id {
			return s.Config
		}
	}
	return domain.RunConfig{}
}

func (d sessionConfigDraft) apply(c domain.RunConfig) domain.RunConfig {
	return domain.RunConfig{
		Model:      runChoice(d.Model, c.Model),
		Effort:     runChoice(d.Effort, c.Effort),
		Permission: runChoice(d.Permission, c.Permission),
		Plan:       runChoice(d.Plan, c.Plan),
		Fast:       runChoice(d.Fast, c.Fast),
	}
}

func runChoice[T comparable](choice *T, accepted T) T {
	if choice != nil {
		return *choice
	}
	return accepted
}

func (m *Model) setSessionConfig(next domain.RunConfig) {
	id := m.SessionID
	ready := m.RunReadiness()
	accepted := m.acceptedSessionConfig(id)
	d := m.sessionConfigDrafts[id]
	current := normalizeRunConfig(d.apply(accepted), ready)
	base := normalizeRunConfig(accepted, ready)
	updateRunChoice(&d.Model, current.Model, next.Model, base.Model)
	updateRunChoice(&d.Effort, current.Effort, next.Effort, base.Effort)
	updateRunChoice(&d.Permission, current.Permission, next.Permission, base.Permission)
	updateRunChoice(&d.Plan, current.Plan, next.Plan, base.Plan)
	updateRunChoice(&d.Fast, current.Fast, next.Fast, base.Fast)
	m.storeSessionConfigDraft(id, d)
}

func updateRunChoice[T comparable](choice **T, current, next, base T) {
	if next != current {
		*choice = &next
		clearRunChoice(choice, base)
	}
}

func (m *Model) reconcileSessionConfig(id string, config domain.RunConfig) {
	d, ok := m.sessionConfigDrafts[id]
	if !ok {
		return
	}
	clearRunChoice(&d.Model, config.Model)
	clearRunChoice(&d.Effort, config.Effort)
	clearRunChoice(&d.Permission, config.Permission)
	clearRunChoice(&d.Plan, config.Plan)
	clearRunChoice(&d.Fast, config.Fast)
	m.storeSessionConfigDraft(id, d)
}

func clearRunChoice[T comparable](choice **T, recorded T) {
	if *choice != nil && **choice == recorded {
		*choice = nil
	}
}

func (m *Model) storeSessionConfigDraft(id string, draft sessionConfigDraft) {
	if draft == (sessionConfigDraft{}) {
		delete(m.sessionConfigDrafts, id)
		return
	}
	m.sessionConfigDrafts[id] = draft
}

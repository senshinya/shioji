package app

import (
	"shioji/internal/domain"
	"slices"
)

// Menu preferences are client presentation, scoped to stable device identity.
// They never change a harness's authoritative catalog or accepted run config.
type ModelMenuPreferences struct {
	Hidden    []string `json:"hidden,omitempty"`
	Favorites []string `json:"favorites,omitempty"`
	Order     []string `json:"order,omitempty"`
}

func (m *Model) ModelPreference(device, harness string) ModelMenuPreferences {
	return m.ModelMenus[device][harness]
}
func (m *Model) SetModelPreferences(device, harness string, p ModelMenuPreferences) {
	if m.ModelMenus == nil {
		m.ModelMenus = map[string]map[string]ModelMenuPreferences{}
	}
	if m.ModelMenus[device] == nil {
		m.ModelMenus[device] = map[string]ModelMenuPreferences{}
	}
	p.Hidden = slices.Clone(p.Hidden)
	p.Favorites = slices.Clone(p.Favorites)
	p.Order = slices.Clone(p.Order)
	m.ModelMenus[device][harness] = p
}
func (m *Model) OrderedModels(device, harness string, models []domain.Model) []domain.Model {
	return m.ModelPreference(device, harness).Ordered(models, true)
}
func (p ModelMenuPreferences) Ordered(models []domain.Model, groupFavorites bool) []domain.Model {
	out := slices.Clone(models)
	rank := func(id string) int {
		if i := slices.Index(p.Order, id); i >= 0 {
			return i
		}
		return len(p.Order)
	}
	slices.SortStableFunc(out, func(a, b domain.Model) int {
		af, bf := slices.Contains(p.Favorites, a.ID), slices.Contains(p.Favorites, b.ID)
		if groupFavorites && af != bf {
			if af {
				return -1
			}
			return 1
		}
		return rank(a.ID) - rank(b.ID)
	})
	return out
}
func (m *Model) ModelsForMenu(device, harness string, models []domain.Model) []domain.Model {
	hidden := m.ModelPreference(device, harness).Hidden
	return slices.DeleteFunc(m.OrderedModels(device, harness, models), func(model domain.Model) bool { return slices.Contains(hidden, model.ID) })
}
func (m *Model) ConnectionDeviceID(key string) string {
	if key == "local" || key == "" {
		return m.LocalDeviceID
	}
	for _, connection := range m.Connections {
		if connection.ID == key {
			return connection.DeviceID
		}
	}
	return ""
}

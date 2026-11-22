package mobileclient

import (
	"errors"
	"maps"
	"slices"
	"time"

	"shioji/internal/domain"
)

type WorkspaceOrder struct {
	Location WorkspaceLocation    `json:"location"`
	Sessions []string             `json:"sessions,omitempty"`
	Inputs   map[string]time.Time `json:"inputs,omitempty"`
}

type NavigationOrder []WorkspaceOrder

func (order NavigationOrder) Locations() []WorkspaceLocation {
	locations := make([]WorkspaceLocation, len(order))
	for i, item := range order {
		locations[i] = item.Location
	}
	return locations
}

func (order NavigationOrder) index(location WorkspaceLocation) int {
	return slices.IndexFunc(order, func(item WorkspaceOrder) bool { return item.Location == location })
}

func (order NavigationOrder) SessionIDs(location WorkspaceLocation) []string {
	if i := order.index(location); i >= 0 {
		return order[i].Sessions
	}
	return nil
}

// Observe learns positions from catalog identities and advances them only on
// newer user-input timestamps. Missing catalog rows retain their sleep/offline positions.
func (order NavigationOrder) Observe(catalog domain.Snapshot) (NavigationOrder, bool) {
	if catalog.DeviceID == "" {
		return order, false
	}
	byWorkspace := make(map[string][]domain.Session, len(catalog.Workspaces))
	for _, session := range catalog.Sessions {
		if session.ID != "" && (session.Context.DeviceID == "" || session.Context.DeviceID == catalog.DeviceID) {
			byWorkspace[session.WorkspaceID] = append(byWorkspace[session.WorkspaceID], session)
		}
	}
	next := slices.Clone(order)
	changed := false
	for _, workspace := range catalog.Workspaces {
		if workspace.ID == "" || workspace.DeviceID != catalog.DeviceID {
			continue
		}
		location := WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}
		i := next.index(location)
		if i < 0 {
			i = len(next)
			next = append(next, WorkspaceOrder{Location: location})
			changed = true
		}
		if updated, moved := next[i].observe(byWorkspace[workspace.ID], catalog.SessionInputTimes, workspace.Archived); moved {
			next[i], changed = updated, true
		}
	}
	if !changed {
		return order, false
	}
	return next, true
}

func (rank WorkspaceOrder) observe(sessions []domain.Session, times map[string]time.Time, archived bool) (WorkspaceOrder, bool) {
	known := make(map[string]bool, len(rank.Sessions))
	for _, id := range rank.Sessions {
		known[id] = true
	}
	var added, promoted []string
	inputsChanged := false
	for _, session := range sessions {
		id := session.ID
		stamp := times[id]
		if !known[id] {
			known[id] = true
			added = append(added, id)
		} else if !archived && !session.Archived && stamp.After(rank.Inputs[id]) {
			promoted = append(promoted, id)
		}
		if stamp.After(rank.Inputs[id]) {
			if !inputsChanged {
				rank.Inputs = maps.Clone(rank.Inputs)
				if rank.Inputs == nil {
					rank.Inputs = make(map[string]time.Time)
				}
				inputsChanged = true
			}
			rank.Inputs[id] = stamp
		}
	}
	if len(added)+len(promoted) == 0 {
		return rank, inputsChanged
	}
	slices.SortStableFunc(promoted, func(a, b string) int { return times[b].Compare(times[a]) })
	moved := make(map[string]bool, len(promoted))
	for _, id := range promoted {
		moved[id] = true
	}
	next := make([]string, 0, len(rank.Sessions)+len(added))
	next = append(next, added...)
	next = append(next, promoted...)
	for _, id := range rank.Sessions {
		if !moved[id] {
			next = append(next, id)
		}
	}
	rank.Sessions = next
	return rank, true
}

func (order NavigationOrder) ReorderWorkspaces(before, after []WorkspaceLocation) (NavigationOrder, error) {
	locations, err := ReorderVisible(order.Locations(), before, after)
	if err != nil {
		return order, err
	}
	byLocation := make(map[WorkspaceLocation]WorkspaceOrder, len(order))
	for _, item := range order {
		byLocation[item.Location] = item
	}
	next := make(NavigationOrder, len(order))
	for i, location := range locations {
		next[i] = byLocation[location]
	}
	return next, nil
}

func (order NavigationOrder) ReorderSessions(location WorkspaceLocation, before, after []string) (NavigationOrder, error) {
	i := order.index(location)
	if i < 0 {
		return order, errors.New("工作区归属已变化，请重新排列")
	}
	sessions, err := ReorderVisible(order[i].Sessions, before, after)
	if err != nil {
		return order, err
	}
	next := slices.Clone(order)
	next[i].Sessions = sessions
	return next, nil
}

// ReorderVisible replaces only the slots edited in the native list. This
// preserves missing and newly arrived rows as well as the ordinary pin positions.
func ReorderVisible[T comparable](full, before, after []T) ([]T, error) {
	stale := errors.New("列表顺序已变化，请重新排列")
	if len(before) != len(after) {
		return nil, stale
	}
	included := make(map[T]bool, len(before))
	for _, value := range before {
		included[value] = true
	}
	if len(included) != len(before) {
		return nil, stale
	}
	seen := make(map[T]bool, len(after))
	for _, value := range after {
		if !included[value] || seen[value] {
			return nil, stale
		}
		seen[value] = true
	}
	next := slices.Clone(full)
	at := 0
	for i, value := range full {
		if included[value] {
			if at >= len(before) || before[at] != value {
				return nil, stale
			}
			next[i] = after[at]
			at++
		}
	}
	if at != len(before) {
		return nil, stale
	}
	return next, nil
}

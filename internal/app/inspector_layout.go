package app

import "slices"

var InspectorViews = []string{"changes", "tasks", "agents", "background"}

func CloneInspectorGroups(in []InspectorGroup) []InspectorGroup {
	out := make([]InspectorGroup, len(in))
	for i, g := range in {
		out[i] = InspectorGroup{Views: slices.Clone(g.Views), Selected: g.Selected}
	}
	return out
}
func SanitizeInspectorGroups(in []InspectorGroup) []InspectorGroup {
	if len(in) == 0 {
		return nil
	}
	out := []InspectorGroup{}
	seen := map[string]bool{}
	for _, g := range in {
		if len(out) == 2 {
			break
		}
		next := InspectorGroup{Selected: g.Selected}
		for _, kind := range g.Views {
			if !slices.Contains(InspectorViews, kind) || seen[kind] {
				continue
			}
			seen[kind] = true
			next.Views = append(next.Views, kind)
		}
		if len(next.Views) > 0 {
			if !slices.Contains(next.Views, next.Selected) {
				next.Selected = next.Views[0]
			}
			out = append(out, next)
		}
	}
	if len(out) == 0 {
		return nil
	}
	for _, kind := range InspectorViews {
		if seen[kind] {
			continue
		}
		index := 0
		if len(out) == 2 && (kind == "tasks" || kind == "background") {
			index = 1
		}
		out[index].Views = append(out[index].Views, kind)
	}
	return out
}
func MoveInspectorView(in []InspectorGroup, kind string, target int) []InspectorGroup {
	out := CloneInspectorGroups(in)
	if target < 0 || target >= len(out) || !slices.Contains(InspectorViews, kind) {
		return out
	}
	for i := range out {
		if i == target {
			continue
		}
		out[i].Views = slices.DeleteFunc(out[i].Views, func(k string) bool { return k == kind })
		if out[i].Selected == kind && len(out[i].Views) > 0 {
			out[i].Selected = out[i].Views[0]
		}
	}
	if !slices.Contains(out[target].Views, kind) {
		out[target].Views = append(out[target].Views, kind)
	}
	out[target].Selected = kind
	return slices.DeleteFunc(out, func(g InspectorGroup) bool { return len(g.Views) == 0 })
}
func SplitInspectorView(in []InspectorGroup, kind string) []InspectorGroup {
	upper := InspectorGroup{}
	for _, g := range in {
		for _, view := range g.Views {
			if view != kind {
				upper.Views = append(upper.Views, view)
			}
		}
	}
	if len(upper.Views) == 0 {
		return CloneInspectorGroups(in)
	}
	upper.Selected = upper.Views[0]
	return []InspectorGroup{upper, {Views: []string{kind}, Selected: kind}}
}

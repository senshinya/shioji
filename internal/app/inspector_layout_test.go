package app

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestFourViewInspectorLayoutPersistsWithoutDuplicates(t *testing.T) {
	m := NewModel()
	m.InspectorLayout.Groups = []InspectorGroup{{Views: []string{"changes", "agents"}, Selected: "agents"}, {Views: []string{"tasks", "background"}, Selected: "background"}}
	merged := MoveInspectorView(m.InspectorLayout.Groups, "tasks", 0)
	merged = MoveInspectorView(merged, "background", 0)
	if len(merged) != 1 || merged[0].Selected != "background" {
		t.Fatal(merged)
	}
	m.InspectorLayout.Groups = SplitInspectorView(merged, "background")
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.InspectorLayout, m.InspectorLayout) {
		t.Fatal("layout lost on restart", restored.InspectorLayout)
	}
	repaired := SanitizeInspectorGroups([]InspectorGroup{{Views: []string{"agents", "agents", "foreign"}, Selected: "foreign"}})
	if len(repaired) != 1 || len(repaired[0].Views) != 4 || repaired[0].Selected != "agents" {
		t.Fatal("invalid persisted tabs not normalized", repaired)
	}
}

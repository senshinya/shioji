package app

import (
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestModelMenuPreferencesPersistByDeviceAndHarnessWithoutChangingAvailability(t *testing.T) {
	m := NewModel()
	models := []domain.Model{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	m.SetModelPreferences("device-a", "one", ModelMenuPreferences{Hidden: []string{"b"}, Favorites: []string{"c"}, Order: []string{"b", "a", "c"}})
	got := m.ModelsForMenu("device-a", "one", models)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Fatal("menu preferences ignored", got)
	}
	if len(m.ModelsForMenu("device-b", "one", models)) != 3 || len(m.ModelsForMenu("device-a", "two", models)) != 3 {
		t.Fatal("preference scope leaked")
	}
	if len(models) != 3 || models[0].ID != "a" {
		t.Fatal("availability mutated")
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	if err := next.Load(path); err != nil {
		t.Fatal(err)
	}
	if got = next.ModelsForMenu("device-a", "one", models); len(got) != 2 || got[0].ID != "c" {
		t.Fatal("preferences lost", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

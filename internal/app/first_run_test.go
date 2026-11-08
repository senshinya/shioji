package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstRunPersistsUntilExplicitCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	m := NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if !m.FirstRun {
		t.Fatal("new installation skipped setup")
	}
	m.Drafts["home"] = "保留草稿"
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	reopened := NewModel()
	if err := reopened.Load(path); err != nil {
		t.Fatal(err)
	}
	if !reopened.FirstRun || reopened.Drafts["home"] != "保留草稿" {
		t.Fatal("unfinished setup or draft lost")
	}
	reopened.FirstRun = false
	if err := reopened.Save(path); err != nil {
		t.Fatal(err)
	}
	final := NewModel()
	if err := final.Load(path); err != nil {
		t.Fatal(err)
	}
	if final.FirstRun {
		t.Fatal("completed setup repeated")
	}
}
func TestLegacyInstallDoesNotEnterFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(`{"deviceId":"existing","drafts":{"home":"keep"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if m.FirstRun || m.Drafts["home"] != "keep" {
		t.Fatal("upgrade replaced existing navigation")
	}
}

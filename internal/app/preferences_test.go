package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClientPreferencesSurviveRestartAlongsideDrafts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{"drafts":{"home":"keep this draft"},"preferences":{"appearance":"dark","interfaceFont":"Arial","replySize":16,"codeFont":"Menlo","reduceMotion":true,"shortcuts":{"send":"Cmd+Enter","newline":"Enter","steer":"Shift+Enter"}}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Drafts      map[string]string
		Preferences struct {
			Appearance, InterfaceFont, CodeFont string
			ReplySize                           int
			ReduceMotion                        bool
			Shortcuts                           struct{ Send, Newline, Steer string }
		}
	}
	if err := json.Unmarshal(got, &state); err != nil {
		t.Fatal(err)
	}
	if state.Preferences.Appearance != "dark" || state.Preferences.InterfaceFont != "Arial" || state.Preferences.ReplySize != 16 || state.Preferences.CodeFont != "Menlo" || !state.Preferences.ReduceMotion {
		t.Fatalf("client preferences lost after restart: %+v", state.Preferences)
	}
	if state.Preferences.Shortcuts.Send != "Cmd+Enter" || state.Preferences.Shortcuts.Newline != "Enter" || state.Preferences.Shortcuts.Steer != "Shift+Enter" || state.Drafts["home"] != "keep this draft" {
		t.Fatal("shortcut choices or independent draft were lost", state)
	}
}

func TestForegroundNotificationsDefaultOnAndPreserveExplicitOptOut(t *testing.T) {
	m := NewModel()
	if !m.Preferences.Notifications.Foreground {
		t.Fatal("foreground notifications are not on by default")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"preferences":{"notifications":{"enabled":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if !m.Preferences.Notifications.Foreground {
		t.Fatal("older settings did not receive the new default")
	}
	m.Preferences.Notifications.Foreground = false
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if restored.Preferences.Notifications.Foreground {
		t.Fatal("explicit opt-out was overwritten by the default")
	}
}

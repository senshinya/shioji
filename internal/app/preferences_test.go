package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMobileComposerBindingsPreserveLegacyAndPersistIndependentChoices(t *testing.T) {
	for _, legacyReturnSends := range []bool{false, true} {
		p := (ClientPreferences{MobileReturnSends: legacyReturnSends}).Normalized()
		plain := "newline"
		if legacyReturnSends {
			plain = "send"
		}
		for binding, want := range map[string]string{
			"Enter": plain, "Cmd+Enter": "send", "Shift+Enter": "newline", "Cmd+Shift+Enter": "steer",
		} {
			if got := p.MobileShortcutAction(binding); got != want {
				t.Errorf("legacy Return sends=%v: %s has action %q, want %q", legacyReturnSends, binding, got, want)
			}
		}
	}
	m := NewModel()
	p := &m.Preferences
	desktop := p.Shortcuts
	if err := p.SetMobileShortcut("send", "Enter"); err == nil || p.MobileComposerShortcuts != nil {
		t.Fatal("a conflict must leave both mobile and desktop preferences intact")
	}
	for _, setting := range []struct{ action, binding string }{
		{"newline", "Alt+Enter"}, {"send", "Enter"}, {"steer", "Ctrl+Enter"},
	} {
		if err := p.SetMobileShortcut(setting.action, setting.binding); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	p = &restored.Preferences
	for binding, want := range map[string]string{
		"Enter": "send", "Alt+Enter": "newline", "Ctrl+Enter": "steer", "Cmd+Enter": "", "Shift+Enter": "",
	} {
		if got := p.MobileShortcutAction(binding); got != want {
			t.Errorf("restored %s has action %q, want %q", binding, got, want)
		}
	}
	if p.Shortcuts != desktop {
		t.Fatal("mobile choices changed desktop shortcuts", p.Shortcuts)
	}
	p.ResetMobileShortcuts()
	if p.MobileShortcuts() != (ComposerShortcuts{Send: "Cmd+Enter", Newline: "Enter", Steer: "Cmd+Shift+Enter"}) ||
		p.MobileShortcutAction("Shift+Enter") != "newline" || p.Shortcuts != desktop {
		t.Fatal("restoring mobile defaults affected other bindings", p)
	}
}

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

func TestLatestToolsDefaultCollapsedAndPreserveSavedChoice(t *testing.T) {
	if !NewModel().Preferences.CollapseLatestTools {
		t.Error("new clients should not auto-expand latest tools")
	}
	for _, tc := range []struct {
		name, state string
		collapsed   bool
	}{
		{"no preferences", `{}`, true},
		{"old preferences", `{"preferences":{"appearance":"dark"}}`, true},
		{"disabled", `{"preferences":{"collapseLatestTools":true}}`, true},
		{"enabled", `{"preferences":{"collapseLatestTools":false}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(tc.state), 0600); err != nil {
				t.Fatal(err)
			}
			m := NewModel()
			if err := m.Load(path); err != nil {
				t.Fatal(err)
			}
			if m.Preferences.CollapseLatestTools != tc.collapsed {
				t.Fatal("incorrect loaded tool default", m.Preferences.CollapseLatestTools)
			}
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			restored := NewModel()
			if err := restored.Load(path); err != nil {
				t.Fatal(err)
			}
			if restored.Preferences.CollapseLatestTools != tc.collapsed {
				t.Fatal("saved tool preference lost after restart")
			}
		})
	}
}

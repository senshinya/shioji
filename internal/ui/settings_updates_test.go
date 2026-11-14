package ui

import (
	"errors"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestAppUpdateSettingsCheckAndPersistChoice(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	s.selectPage("about")
	on, checks, fail := true, 0, false
	s.AppUpdates = &AppUpdateControls{Enabled: func() bool { return true }, AutomaticChecks: func() bool { return on }, SetAutomaticChecks: func(value bool) error {
		if fail {
			return errors.New("磁盘不可写")
		}
		on = value
		return nil
	}, Check: func() { checks++ }}
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	if checks != 0 {
		t.Fatal("opening About started a manual check")
	}
	if err := ui.Click("检查 App 更新"); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatal("manual update entry is not connected")
	}
	ui.Click("自动检查 App 更新")
	ui.Advance(time.Second)
	if on {
		t.Fatal("automatic check preference was not saved")
	}
	fail = true
	ui.Click("自动检查 App 更新")
	ui.Advance(time.Second)
	if on || !ui.HasText("无法保存更新设置：磁盘不可写") {
		t.Fatal("save failure was hidden", ui.Texts())
	}
	fail = false
	ui.Click("自动检查 App 更新")
	ui.Advance(time.Second)
	if !on || s.error != "" {
		t.Fatal("cannot retry update preference")
	}
}

func TestAppUpdateSettingsDisableAutomaticChecksWhenUnavailable(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	s.selectPage("about")
	s.AppUpdates = &AppUpdateControls{Enabled: func() bool { return false }, AutomaticChecks: func() bool { return true }, SetAutomaticChecks: func(bool) error { t.Fatal("unavailable updater preference changed"); return nil }, Check: func() {}}
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	ui.Click("自动检查 App 更新")
}

func TestAppUpdateSettingsNativeEvidence(t *testing.T) {
	if os.Getenv("SHIOJI_SKILL_SNAPSHOTS") == "" {
		t.Skip("native evidence")
	}
	s := NewSettings(New(layoutModel(), nil, ""))
	s.selectPage("about")
	s.AppUpdates = &AppUpdateControls{Enabled: func() bool { return true }, AutomaticChecks: func() bool { return true }, SetAutomaticChecks: func(bool) error { return nil }, Check: func() {}}
	ui := n.NewTester(s.View, 968, 756)
	ui.SetDark(false)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "app-updates-light.png")
}

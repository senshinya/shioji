package ui

import (
	"errors"
	"slices"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func permissionSettingsUI(t *testing.T) (*Settings, *n.Tester) {
	t.Helper()
	s := NewSettings(New(layoutModel(), nil, ""))
	s.selectPage("about")
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	return s, ui
}

func TestFullDiskAccessSettingOpensOnlyItsSystemPage(t *testing.T) {
	s, ui := permissionSettingsUI(t)
	if len(ui.OpenedURLs()) != 0 {
		t.Fatal("displaying settings opened system preferences without a click")
	}
	before := s.owner.Model.Preferences
	if err := ui.Click("授予完全磁盘访问权限"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !slices.Equal(ui.OpenedURLs(), []string{"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"}) {
		t.Fatal("permission action did not open the Full Disk Access page", ui.OpenedURLs())
	}
	if s.owner.Model.Preferences != before || len(s.owner.Model.Pending) != 0 || ui.HasText("已授权") {
		t.Fatal("opening system settings changed app preferences or claimed authorization")
	}
}

func TestFullDiskAccessSettingReportsOpenFailureAndRetries(t *testing.T) {
	s, ui := permissionSettingsUI(t)
	ui.FailOpenURL(errors.New("系统设置不可用"))
	if err := ui.Click("授予完全磁盘访问权限"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("无法打开完全磁盘访问权限设置：系统设置不可用") {
		t.Fatal("failed system settings launch has no visible error", ui.Texts())
	}
	ui.FailOpenURL(nil)
	ui.Click("授予完全磁盘访问权限")
	ui.Advance(time.Second)
	if s.error != "" || len(ui.OpenedURLs()) != 2 {
		t.Fatal("permission action cannot retry after launch failure", s.error, ui.OpenedURLs())
	}
}

func TestFullDiskAccessSettingCanBeFoundFromSearch(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	ui := n.NewTester(s.View, 968, 756)
	ui.Click("搜索设置")
	ui.Type("完全磁盘")
	ui.Advance(time.Second)
	if err := ui.Click("关于 · 授予完全磁盘访问权限"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if s.page != "about" || s.query != "" || !ui.HasText("授予完全磁盘访问权限") || len(ui.OpenedURLs()) != 0 {
		t.Fatal("search did not navigate to the permission setting", s.page, s.query, ui.OpenedURLs())
	}
}

// The native boundary can fail before System Settings or its guide opens.
// A failed attempt must stay visible and a retry must clear that failure.
func TestFullDiskAccessGuideFailureCanBeRetried(t *testing.T) {
	s, ui := permissionSettingsUI(t)
	failed := true
	s.AuthorizeFullDiskAccess = func(background bool) error {
		if background {
			t.Fatal("main App action selected the background Runtime")
		}
		if failed {
			return errors.New("授权引导库缺失")
		}
		return nil
	}
	ui.Click("授予完全磁盘访问权限")
	ui.Advance(time.Second)
	if !ui.HasText("无法打开完全磁盘访问权限设置：授权引导库缺失") {
		t.Fatal("native guide failure is not visible", ui.Texts())
	}
	failed = false
	ui.Click("授予完全磁盘访问权限")
	ui.Advance(time.Second)
	if s.error != "" || len(ui.OpenedURLs()) != 0 {
		t.Fatal("guide retry kept the error or bypassed the native guide", s.error, ui.OpenedURLs())
	}
}

func TestFullDiskAccessRuntimeActionUsesInstalledRuntime(t *testing.T) {
	s, ui := permissionSettingsUI(t)
	if ui.HasText("为 Shioji Runtime 授权") {
		t.Fatal("uninstalled Runtime has an authorization action")
	}
	installed := true
	s.BackgroundRuntimeAvailable = func() bool { return installed }
	s.AuthorizeFullDiskAccess = func(background bool) error {
		if !background {
			t.Fatal("Runtime action selected the main App")
		}
		return errors.New("Runtime 暂不可用")
	}
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("为 Shioji Runtime 授权"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("无法打开完全磁盘访问权限设置：Runtime 暂不可用") || len(ui.OpenedURLs()) != 0 {
		t.Fatal("Runtime action bypassed its native target or hid the failure", ui.Texts())
	}
	installed = false
	ui.Frame()
	if ui.HasText("为 Shioji Runtime 授权") {
		t.Fatal("removed Runtime is still offered for authorization")
	}
}

package ui

import "testing"

func TestNotificationVisibilityFollowsActualPanesAndApplicationFocus(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Preferences.Notifications.Foreground = false
	v.viewportWidth = 1440
	if !v.foregroundNotificationSuppressed("s", true) || v.foregroundNotificationSuppressed("s", false) || v.foregroundNotificationSuppressed("background", true) {
		t.Fatal("notification policy lost foreground/session distinction")
	}
	v.Model.SecondarySessionID = "right"
	if !v.foregroundNotificationSuppressed("right", true) {
		t.Fatal("visible second pane would produce redundant notification")
	}
	v.viewportWidth = 850
	if v.foregroundNotificationSuppressed("right", true) {
		t.Fatal("hidden narrow pane was treated as visible")
	}
	v.Model.FocusSecondary = true
	if v.foregroundNotificationSuppressed("s", true) || !v.foregroundNotificationSuppressed("right", true) {
		t.Fatal("narrow focus was not respected")
	}
	v.devices = true
	if v.foregroundNotificationSuppressed("right", true) {
		t.Fatal("device page was treated as reading a session")
	}
	v.devices = false
	v.Model.Preferences.Notifications.Foreground = true
	if v.foregroundNotificationSuppressed("right", true) {
		t.Fatal("enabled foreground option did not allow notification")
	}
}

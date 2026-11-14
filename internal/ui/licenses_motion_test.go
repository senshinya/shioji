package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func licenseSettingsUI(t *testing.T, reduced bool) (*Settings, *n.Tester) {
	t.Helper()
	v := New(layoutModel(), nil, "")
	v.Model.Preferences.ReduceMotion = reduced
	s := NewSettings(v)
	s.selectPage("about")
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	return s, ui
}

func TestLicenseNavigationSlidesInBothDirections(t *testing.T) {
	_, ui := licenseSettingsUI(t, false)
	for _, step := range []struct {
		action, label string
		forward       bool
	}{
		{"查看第三方许可", "返回关于", true},
		{"许可：" + bundledNotices[0].Module, "返回第三方许可", true},
		{"返回第三方许可", "返回关于", false},
		{"返回关于", "查看第三方许可", false},
	} {
		if err := ui.Click(step.action); err != nil {
			t.Fatal(err)
		}
		ui.Advance(60 * time.Millisecond)
		mid, ok := ui.Find(step.label)
		if !ok {
			t.Fatal("missing incoming page", step.action, ui.Texts())
		}
		ui.Advance(400 * time.Millisecond)
		end, ok := ui.Find(step.label)
		if !ok {
			t.Fatal("missing settled page", step.action)
		}
		if step.forward && mid.X <= end.X+1 || !step.forward && mid.X >= end.X-1 && mid.W >= end.W-1 {
			t.Errorf("license page did not slide in the expected direction: %s mid=%v end=%v", step.action, mid, end)
		}
	}
}

func TestLicenseNavigationReductionAndCategoryReset(t *testing.T) {
	s, ui := licenseSettingsUI(t, true)
	for _, step := range []struct{ action, incoming string }{
		{"查看第三方许可", "返回关于"},
		{"许可：" + bundledNotices[0].Module, "返回第三方许可"},
		{"返回第三方许可", "返回关于"},
		{"返回关于", "查看第三方许可"},
	} {
		if err := ui.Click(step.action); err != nil {
			t.Fatal(err)
		}
		ui.Frame()
		mid, ok := ui.Find(step.incoming)
		if !ok {
			t.Fatal("reduced motion did not show the selected page", step.incoming)
		}
		ui.Advance(400 * time.Millisecond)
		end, _ := ui.Find(step.incoming)
		if mid != end {
			t.Fatal("reduced motion retained a sliding license transition", step.action, mid, end)
		}
	}
	ui.Click("查看第三方许可")
	ui.Click("外观")
	ui.Click("关于")
	if s.licenses || s.license != -1 || !ui.HasText("查看第三方许可") {
		t.Fatal("category change retained license navigation")
	}
}

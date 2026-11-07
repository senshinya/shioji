package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestRunMenuUsesSelectedHarnessCatalogAndHidesUnsupportedControls(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.WorkerMinor = 10
	m.Drafts["home"] = "keep draft"
	m.Readiness = domain.Readiness{HarnessID: "alpha", Harnesses: []domain.HarnessInfo{
		{ID: "alpha", Name: "Alpha Agent", State: "ready", Models: []domain.Model{{ID: "a", Name: "A model", Default: true, Efforts: []string{"normal"}, DefaultEffort: "normal", Fast: true}}, Permissions: []string{"limited"}, Plan: true},
		{ID: "beta", Name: "Beta Agent", State: "ready", Models: []domain.Model{{ID: "b", Name: "B model", Default: true}}, Permissions: []string{"ask"}, PermissionChoices: []domain.OptionChoice{{ID: "ask", Name: "先询问"}}},
	}}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("运行配置"); err != nil {
		t.Fatal(err)
	}
	if !ui.HasText("Alpha Agent") || ui.HasText("Codex") {
		t.Fatal("menu contains hardcoded harness", ui.Texts())
	}
	if err := ui.Click("Agent"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("Beta Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	if m.HarnessID() != "beta" || m.Config().Model != "b" || m.Drafts["home"] != "keep draft" {
		t.Fatal("selection did not bind run configuration")
	}
	if !ui.HasText("Beta Agent") || ui.HasText("推理强度") || ui.HasText("Plan") || ui.HasText("Fast") {
		t.Fatal("unsupported controls remain visible", ui.Texts())
	}
	if err := ui.Click("运行配置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	if !ui.HasText("先询问") {
		t.Fatal("permission label did not come from catalog", ui.Texts())
	}
}

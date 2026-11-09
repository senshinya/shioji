package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestUnavailableModelUsesRunControlWithoutConsumingDraft(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, home := range []bool{false, true} {
			m := layoutModel()
			if home {
				m.SessionID = ""
			}
			m.Config()
			key := m.DraftKey()
			m.Drafts[key] = "保留这份草稿"
			v := New(m, app.NewController(""), "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			m.Readiness.Models = []domain.Model{{ID: "replacement", Name: "可用的新模型", Default: true}}
			ui.Frame()
			ui.Advance(240 * time.Millisecond)
			if ui.HasText("当前模型已不可用，请更换模型") || ui.HasText("更换模型") {
				t.Fatal("model removal adds a separate notice instead of using the model control", dark, home, ui.Texts())
			}
			if err := ui.Click("发送"); err != nil {
				t.Fatal(err)
			}
			if m.HasPendingInput() || m.Drafts[key] != "保留这份草稿" {
				t.Fatal("unavailable model accepted a send or consumed the draft")
			}
			if err := ui.Click("运行配置"); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			ui.Advance(240 * time.Millisecond)
			if err := ui.Click("可用的新模型"); err != nil {
				t.Fatal("model control did not open the model picker", err)
			}
			ui.Frame()
			ui.Advance(250 * time.Millisecond)
			if m.Config().Model != "replacement" || m.Drafts[key] != "保留这份草稿" {
				t.Fatal("model switch lost draft or failed", m.Config(), m.Drafts)
			}
			if ui.HasText("当前模型已不可用，请更换模型") {
				t.Fatal("availability notice did not retire after choice")
			}
		}
	}
}

func TestStaleCatalogCannotChangeRunModel(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Config()
	key := m.DraftKey()
	original := m.Config().Model
	m.Readiness.State = domain.RuntimeUnavailable
	m.Readiness.Models = []domain.Model{{ID: "stale", Name: "旧模型"}}
	v := New(m, app.NewController(""), "")
	v.runMenu = runMenuModels
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("旧模型")
	ui.Frame()
	if m.Configs[key].Model != original {
		t.Fatal("stale model remained selectable", m.Configs[key])
	}
}

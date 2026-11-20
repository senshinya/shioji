package ui

import (
	"testing"

	native "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestComposerFollowsSharedSessionModel(t *testing.T) {
	opened, fresh := layoutModel(), layoutModel()
	for _, m := range []*app.Model{opened, fresh} {
		m.Readiness.Models = []domain.Model{{ID: "first-model", Default: true}, {ID: "next-model"}}
		m.Snapshot.Sessions[0].Config.Model = "first-model"
	}
	openedUI := native.NewTester(New(opened, nil, "").View, 1000, 650)
	if _, ok := openedUI.Find("first-model"); !ok {
		t.Fatal("initial composer did not show the session's first model")
	}
	changed := opened.Snapshot.Sessions[0]
	changed.Revision++
	changed.Config.Model = "next-model"
	if err := opened.ApplyEvent(domain.Event{Sequence: 1, SessionID: changed.ID, Session: &changed}); err != nil {
		t.Fatal(err)
	}
	snapshot := fresh.Snapshot
	snapshot.Sequence = 1
	snapshot.Sessions[0] = changed
	fresh.ApplySnapshot(snapshot)
	freshUI := native.NewTester(New(fresh, nil, "").View, 1000, 650)
	openedUI.Frame()
	saveNativeSkillImage(t, openedUI, "already-open.png")
	saveNativeSkillImage(t, freshUI, "freshly-opened.png")
	for name, ui := range map[string]*native.Tester{"already-open": openedUI, "freshly-opened": freshUI} {
		if _, ok := ui.Find("next-model"); !ok {
			t.Errorf("%s composer did not show the new model", name)
		}
	}
}

package ui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestSkillSearchFiltersLoadedCatalogWithoutAnotherDeviceRequest(t *testing.T) {
	v, ui := referenceComposer(t)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("/rev")
	waitNative(t, ui, func() bool {
		return v.fileMention != nil && v.fileMention.phase == mentionShowing && len(v.fileMention.skills) > 0
	})
	state := v.fileMention
	ui.Type("iew-tree")
	ui.Frame()
	if v.fileMention != state || state.phase != mentionShowing || len(state.skills) != 1 || state.skills[0].Name != "review-tree" {
		t.Fatalf("typing a filter restarted device discovery: phase=%v, skills=%+v", state.phase, state.skills)
	}
	ui.Type("-missing")
	ui.Frame()
	if state.phase != mentionShowing || len(state.skills) != 0 {
		t.Fatal("local filtering kept stale matches", state.skills)
	}
	for range len("-missing") {
		ui.Key(0, n.KeyBackspace)
	}
	ui.Frame()
	if state.phase != mentionShowing || len(state.skills) != 1 || state.skills[0].Name != "review-tree" {
		t.Fatal("broadening the filter lost the loaded catalog", state.skills)
	}
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 1 })
	if !ui.Focused("消息输入") || len(v.Model.Pending) != 0 {
		t.Fatal("filter selection stole focus or sent the draft")
	}
	ui.Advance(150 * time.Millisecond)
}

func TestSkillSearchDropsCatalogWhenHomeExecutionDirectoryChanges(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/review-tree")
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	old := v.fileMention
	conn, err := net.Dial("unix", filepath.Join(v.ControllerDirectory(), "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer client.Close()
	workspace := v.Model.Snapshot.Workspaces[0]
	workspace.Folders = []string{t.TempDir()}
	var updated domain.Workspace
	if err := client.Call(context.Background(), "workspace.update", workspace, &updated); err != nil {
		t.Fatal(err)
	}
	v.Model.Snapshot.Workspaces[0] = updated
	ui.Frame()
	if v.fileMention == old {
		t.Fatal("home reused Skills from its previous execution directory")
	}
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	if len(v.fileMention.skills) != 0 {
		t.Fatal("new execution directory retained the previous project's Skill", v.fileMention.skills)
	}
}

func TestSkillSearchReopensWithFreshDeviceCatalog(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/rev")
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	ui.Key(0, n.KeyEscape)
	for range len("/rev") {
		ui.Key(0, n.KeyBackspace)
	}
	root := v.Model.Snapshot.Workspaces[0].Folders[0]
	skill := filepath.Join(root, ".agents", "skills", "new-device-skill")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: new-device-skill\ndescription: Newly installed skill\n---\n\nCheck new state.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ui.Type("/new-device-skill")
	waitNative(t, ui, func() bool {
		return v.fileMention != nil && v.fileMention.phase == mentionShowing && len(v.fileMention.skills) == 1 && v.fileMention.skills[0].Name == "new-device-skill"
	})
}

func TestSkillSearchKeepsInFlightDiscoveryAndFiltersLatestQuery(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/rev")
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	state := v.fileMention
	state.skillsLoaded = false
	v.searchMention(state)
	// Hold the actual worker completion until after more native input. This
	// exercises a slow response without delaying the worker or using a fake list.
	select {
	case complete := <-v.Controller.Updates:
		ui.Type("iew-tree")
		ui.Frame()
		if state.phase != mentionSearching {
			t.Fatal("query edits restarted an in-flight catalog request", state.phase)
		}
		complete(v.Model)
	case <-time.After(8 * time.Second):
		t.Fatal("device catalog request did not complete")
	}
	ui.Frame()
	if state.phase != mentionShowing || len(state.skills) != 1 || state.skills[0].Name != "review-tree" {
		t.Fatal("in-flight response did not filter the latest query", state.phase, state.skills)
	}
}

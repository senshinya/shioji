package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestArchiveSingleDeleteDoesNotBlockAndOnlyRetriesThatSession(t *testing.T) {
	v, ui := archiveViewFixture()
	session := v.Model.Snapshot.Sessions[0]
	v.archive.deletion = &archiveDeleteUI{connection: "local", workspace: v.Model.Snapshot.Workspaces[0], session: session, open: true}
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("彻底删除")
	j := nextArchiveDelete(t, v)
	ui.Advance(time.Second)
	if v.archive.deletion.open || !ui.HasText("正在删除") {
		t.Fatal("single deletion still blocks the interface")
	}
	j.Apply(v.Model, nil, errors.New("connection lost"))
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("重试失败项"); err != nil {
		t.Fatal(err)
	}
	retry := nextArchiveDelete(t, v)
	if retry.Input.(protocol.ArchiveDeleteInput) != j.Input.(protocol.ArchiveDeleteInput) {
		t.Fatal("retry changed the captured deletion intent")
	}
	body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{session.ID}})
	retry.Apply(v.Model, body, nil)
	ui.Frame()
	if len(v.Model.Snapshot.Sessions) != 0 {
		t.Fatal("single retry did not remove its archive")
	}
}

func TestArchiveBackgroundDeleteNativeStates(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := archiveViewFixture()
		ui.SetDark(dark)
		for i := 0; i < 15; i++ {
			v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: fmt.Sprint("older-", i), WorkspaceID: "w", Title: fmt.Sprint("历史会话 ", i), Archived: true, Revision: 1})
		}
		ui.Frame()
		ui.Click("删除全部")
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("confirm-dark-%v.png", dark))
		ui.Click("彻底删除")
		nextArchiveDelete(t, v).Apply(v.Model, nil, errors.New("所属设备已断开连接，请重连后重试"))
		ui.Frame()
		ui.Advance(time.Second)
		if v.archive.deletion.open || !ui.HasText("正在删除 1 / 16") {
			t.Fatal("batch progress did not remain inline")
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("pending-dark-%v.png", dark))
		for v.archive.deletion.busy {
			j := nextArchiveDelete(t, v)
			id := j.Input.(protocol.ArchiveDeleteInput).SessionID
			body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{id}})
			j.Apply(v.Model, body, nil)
		}
		ui.Frame()
		ui.Advance(time.Second)
		if !ui.HasText("1 项未删除") || v.archive.deletion.open {
			t.Fatal("partial failure reopened a modal")
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("retry-dark-%v.png", dark))
	}
}

func TestArchiveDeletionContinuesAfterLeavingPage(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Drafts["home"] = "keep this draft"
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "second", WorkspaceID: "w", Title: "Second", Archived: true, Revision: 1})
	ui.Click("删除全部")
	ui.Advance(time.Second)
	ui.Click("彻底删除")
	first := nextArchiveDelete(t, v)
	firstID := first.Input.(protocol.ArchiveDeleteInput).SessionID
	ui.Advance(time.Second)
	if ui.HasText("彻底删除全部归档会话？") {
		t.Fatal("pending deletion still blocks the interface with its confirmation")
	}
	if !ui.HasText("正在删除") {
		t.Fatal("background deletion has no progress")
	}
	if err := ui.Click("新建会话"); err != nil {
		t.Fatal("cannot leave while deletion is pending:", err)
	}
	ui.Advance(time.Second)
	first.Apply(v.Model, nil, errors.New("device disconnected"))
	second := nextArchiveDelete(t, v)
	secondID := second.Input.(protocol.ArchiveDeleteInput).SessionID
	body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{secondID}})
	second.Apply(v.Model, body, nil)
	ui.Advance(time.Second)
	if v.archive.open || ui.HasText("彻底删除全部归档会话？") || v.Model.Drafts["home"] != "keep this draft" {
		t.Fatal("background result interrupted navigation or lost the draft")
	}
	v.openArchive()
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("device disconnected") {
		t.Fatal("background failure was lost", v.archive.deletion, ui.Texts())
	}
	if err := ui.Click("重试失败项"); err != nil {
		t.Fatal("failed target cannot be retried:", err)
	}
	retry := nextArchiveDelete(t, v)
	if retry.Input.(protocol.ArchiveDeleteInput).SessionID != firstID || firstID == secondID {
		t.Fatal("retried a successful or uncaptured target")
	}
	body, _ = json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{firstID}})
	retry.Apply(v.Model, body, nil)
	ui.Advance(time.Second)
	if len(v.Model.Snapshot.Sessions) != 0 {
		t.Fatal("retry did not finish the captured batch")
	}
}

package ui

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func nextArchiveDelete(t *testing.T, v *View) app.Job {
	t.Helper()
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.delete" || j.Method == "workspace.delete" {
			return j
		}
	}
	t.Fatal("no archive deletion submitted")
	return app.Job{}
}

func TestArchiveDeleteAllCapturesUnfilteredListAndOnlyRetriesFailures(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions,
		domain.Session{ID: "other", WorkspaceID: "w", Title: "Hidden by search", Archived: true, Revision: 7},
		domain.Session{ID: "keep", WorkspaceID: "w", Title: "Not archived", Revision: 9})
	v.archive.query = "Layout"
	ui.Frame()
	if err := ui.Click("删除全部"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("全部 2 个归档会话将无法恢复") {
		t.Fatal("confirmation did not disclose unfiltered scope", ui.Texts())
	}
	ui.Click("取消")
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.delete" {
			t.Fatal("cancel submitted deletion")
		}
	}
	ui.Click("删除全部")
	ui.Advance(time.Second)
	// A newly arrived archive is outside the confirmation's captured objects.
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "late", WorkspaceID: "w", Title: "Late archive", Archived: true, Revision: 2})
	ui.Click("彻底删除")
	first := nextArchiveDelete(t, v)
	firstID := first.Input.(protocol.ArchiveDeleteInput).SessionID
	first.Apply(v.Model, nil, errors.New("still running"))
	second := nextArchiveDelete(t, v)
	secondID := second.Input.(protocol.ArchiveDeleteInput).SessionID
	if firstID == secondID || (firstID != "s" && firstID != "other") || (secondID != "s" && secondID != "other") {
		t.Fatal("wrong captured targets", firstID, secondID)
	}
	body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{secondID}})
	second.Apply(v.Model, body, nil)
	ui.Frame()
	if !ui.HasText("still running") || v.archive.deletion == nil || !v.archive.deletion.open {
		t.Fatal("partial failure hidden")
	}
	ui.Click("重试失败项")
	retry := nextArchiveDelete(t, v)
	if retry.Input.(protocol.ArchiveDeleteInput).SessionID != firstID {
		t.Fatal("retried successful or unconfirmed target")
	}
	body, _ = json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{firstID}})
	retry.Apply(v.Model, body, nil)
	ui.Frame()
	if len(v.Model.Snapshot.Sessions) != 2 {
		t.Fatal("deleted unarchived or late session", v.Model.Snapshot.Sessions)
	}
}

func TestArchiveDeleteAllWorkspacesCapturesWorkspaceRevision(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "old", Name: "Old", Archived: true, Revision: 12})
	v.archive.tab = "workspaces"
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("删除全部"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	ui.Click("彻底删除")
	j := nextArchiveDelete(t, v)
	in := j.Input.(protocol.ArchiveDeleteInput)
	if j.Method != "workspace.delete" || in.WorkspaceID != "old" || in.Revision != 12 || in.SessionID != "" {
		t.Fatal("wrong workspace target", j)
	}
}

func TestArchiveDeleteAllRoutesEachOwningDevice(t *testing.T) {
	m := deviceUIModel()
	for key, state := range m.DeviceStates {
		state.WorkerMinor = 44
		state.Snapshot.Sessions = []domain.Session{{ID: "archived-" + key, WorkspaceID: state.Snapshot.Workspaces[0].ID, Title: key, Archived: true, Status: "completed", Revision: 3}}
		m.DeviceStates[key] = state
	}
	m.RefreshDevices()
	v := New(m, app.NewController(""), "")
	v.openArchive()
	a := v.archiveState()
	targets := v.archiveDeleteTargets(a)
	if len(targets) < 2 {
		t.Fatal("fixture must span two devices", targets)
	}
	d := &archiveDeleteUI{batch: true, targets: targets, count: len(targets), open: true}
	a.deletion = d
	v.deleteArchive(d)
	for d.busy {
		j := nextArchiveDelete(t, v)
		in := j.Input.(protocol.ArchiveDeleteInput)
		state := m.DeviceStates[j.ConnectionID]
		found := false
		for _, s := range state.Snapshot.Sessions {
			if s.ID == in.SessionID {
				found = true
			}
		}
		if !found || !in.CloseRuntime || in.OperationID == "" {
			t.Fatal("wrong device or missing explicit shutdown consent", j)
		}
		body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: state.Snapshot.DeviceID, SessionIDs: []string{in.SessionID}})
		m.ApplyDeviceUpdate(j.ConnectionID, func(p *app.Model) { j.Apply(p, body, nil) })
	}
	if d.open || len(m.ArchivedSessions("", "")) != 0 {
		t.Fatal("multi-device deletion retained records", d.error)
	}
}

func TestArchiveDeleteAllDoesNotDispatchOfflineTargets(t *testing.T) {
	v, _ := archiveViewFixture()
	v.Model.Connected = false
	d := &archiveDeleteUI{batch: true, open: true, targets: []archiveDeleteTarget{{connection: "local", title: "Offline", method: "session.delete", input: protocol.ArchiveDeleteInput{SessionID: "s", Revision: 1}}}}
	v.deleteArchive(d)
	if d.busy || !d.open || len(d.failed) != 1 || d.error == "" {
		t.Fatal("offline deletion was not retained", d)
	}
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.delete" {
			t.Fatal("offline mutation dispatched")
		}
	}
}

func TestArchiveDeleteUsesSingleActionAndAcceptsCleanupWarning(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.WorkerMinor = 44
	ui.Click("删除全部")
	ui.Advance(time.Second)
	if ui.HasText("强制删除") {
		t.Fatal("duplicate force deletion action retained")
	}
	if err := ui.Click("彻底删除"); err != nil {
		t.Fatal(err)
	}
	j := nextArchiveDelete(t, v)
	body, _ := json.Marshal(j.Input)
	var fields map[string]any
	json.Unmarshal(body, &fields)
	if fields["force"] == true || fields["closeRuntime"] != true {
		t.Fatal("force consent not sent", string(body))
	}
	body, _ = json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{"s"}, Warning: "后台将继续清理"})
	j.Apply(v.Model, body, nil)
	ui.Frame()
	if v.archive.deletion != nil && v.archive.deletion.open || len(v.Model.Snapshot.Sessions) != 0 {
		t.Fatal("warning blocked accepted force deletion")
	}
	if !ui.HasText("后台将继续清理") {
		t.Fatal("accepted deletion warning was hidden", ui.Texts())
	}
}

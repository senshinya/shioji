package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestSessionContextRenameTargetsSessionAndKeepsMessageDraft(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 5
	m.Drafts["session:s"] = "keep ordinary draft"
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.RightClick("Layout"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("重命名"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if err := ui.Click("新的会话名称"); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("新的任务名称")
	if err := ui.Click("保存名称"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "session.rename" {
			in := job.Input.(protocol.SessionChangeInput)
			if in.SessionID != "s" || in.Title != "新的任务名称" || job.ConnectionID != "local" {
				t.Fatal("rename bound to wrong session", job)
			}
			found = true
		}
	}
	if !found || m.Drafts["session:s"] != "keep ordinary draft" {
		t.Fatal("rename lost request or ordinary draft")
	}
}

func TestSidebarDoubleClickOpensSessionRename(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 5
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	row, _ := ui.Find("Layout")
	ui.ClickAt(row.X+70, row.Y+row.H/2)
	ui.Advance(80 * time.Millisecond)
	ui.ClickAt(row.X+70, row.Y+row.H/2)
	if _, ok := ui.Find("新的会话名称"); !ok {
		t.Fatal("double click did not open rename")
	}
}

func TestRenameConflictReloadIsExplicitAndKeepsMessageDraft(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 5
	m.Drafts["session:s"] = "ordinary draft"
	m.Snapshot.Sessions[0].Title = "Other client title"
	m.Snapshot.Sessions[0].TitleRevision = 2
	v := New(m, app.NewController(""), "")
	v.rename = &sessionRename{Session: domain.Session{ID: "s", WorkspaceID: "w", TitleRevision: 1}, Title: "user edit", Error: "changed", Connection: "local", Open: true}
	ui := n.NewTester(v.View, 1440, 900)
	if v.rename.Title != "user edit" {
		t.Fatal("conflict silently replaced local edit")
	}
	if err := ui.Click("重新载入会话名称"); err != nil {
		t.Fatal(err)
	}
	if v.rename.Title != "Other client title" || v.rename.Session.TitleRevision != 2 || m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("explicit reload used stale title or changed ordinary draft")
	}
}
func TestWorkspaceEditorPromotesReplacementOnPrimaryRemoval(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 5
	m.Snapshot.Workspaces[0].Folders = []string{"/remote/old", "/remote/new"}
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.RightClick("Example"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("工作区设置"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if err := ui.Click("移除目录：/remote/old"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("保存工作区"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "workspace.update" {
			w := job.Input.(domain.Workspace)
			if len(w.Folders) != 1 || w.Folders[0] != "/remote/new" {
				t.Fatal("removed primary not replaced", w)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("workspace editor did not save")
	}
}

func TestArchivingSidebarRowClosesItsActualHeight(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "other", WorkspaceID: "w", Title: "Another", Status: "completed"})
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	before, _ := ui.Find("Another")
	m.Snapshot.Sessions[0].Archived = true
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("Another")
	ui.Advance(250 * time.Millisecond)
	after, _ := ui.Find("Another")
	if !(after.Y < middle.Y && middle.Y < before.Y) {
		t.Fatal("archive row disappeared without a height transition", before, middle, after)
	}
}
func TestArchivedSessionIsHiddenButRemainsReadableWithDraftAndRestore(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 5
	m.Snapshot.Sessions[0].Archived = true
	d := m.Details["s"]
	d.Session.Archived = true
	m.Details["s"] = d
	m.Drafts["session:s"] = "keep archived draft"
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if _, ok := ui.Find("恢复会话"); !ok {
		t.Fatal("archived conversation has no explicit restore")
	}
	if !ui.HasText("Aligned reply") || m.Drafts["session:s"] != "keep archived draft" {
		t.Fatal("archive hid history or discarded draft")
	}
}

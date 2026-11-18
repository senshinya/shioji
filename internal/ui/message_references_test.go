package ui

import (
	"context"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

// Real-worker UI integration uses this test executable as the managed
// launcher, just as worker integration tests do.
func TestMain(m *testing.M) {
	if handled, err := managedexec.ChildMain(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSentInlineReferencesRenderAndCopyInPlace(t *testing.T) {
	v, ui, item := historyFixture()
	enableReferenceHistory(v)
	item.Text = "用  检查 。"
	item.Skills = []domain.SkillReference{{ID: "check", Name: "check"}}
	item.Attachments = []domain.Attachment{{ID: "file", Name: "main.go", SourcePath: "/project/main.go", MIME: "text/plain", Size: 1, SHA256: strings.Repeat("0", 64)}}
	item.ReferencePositions = []domain.ReferencePosition{{SkillID: "check", Position: 3}, {AttachmentID: "file", Position: 8}}
	d := v.Model.Details["s"]
	d.Items[0] = item
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	skill, ok := ui.Find("Skill：check")
	if !ok {
		t.Fatal("sent Skill is not an inline reference")
	}
	file, ok := ui.Find("预览引用：main.go")
	if !ok || file.X <= skill.X || file.Y != skill.Y {
		t.Fatal("sent references lost their text positions", skill, file)
	}
	ui.Click("复制消息")
	if got := ui.Clipboard(); got != "用 check 检查 main.go。" {
		t.Fatal("copy dropped names or changed order", got)
	}
}

func TestHistoryInlineEditorRestoresAndDeletesReferences(t *testing.T) {
	v, ui, item := historyFixture()
	enableReferenceHistory(v)
	item.Text = "用  检查"
	item.Attachments = []domain.Attachment{{ID: "file", Name: "main.go", SourcePath: "/project/main.go", MIME: "text/plain", Size: 1, SHA256: strings.Repeat("0", 64)}}
	item.ReferencePositions = []domain.ReferencePosition{{AttachmentID: "file", Position: 3}}
	d := v.Model.Details["s"]
	d.Items[0] = item
	v.Model.Details["s"] = d
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	key := v.Model.HistoryDraftKey()
	b := v.composerDocuments[key]
	if b == nil || b.doc.Text().String() != "用 \uFFFC 检查" {
		t.Fatal("history editor did not restore native inline document", key, b, v.Model.Error, v.Model.HistoryEdits, v.composerDocuments)
	}
	ui.Click("编辑历史消息")
	b.doc.SetSelection(3, 3)
	ui.Frame()
	ui.Key(0, n.KeyBackspace)
	if len(v.Model.Attachments[key]) != 0 || v.Model.Drafts[key] != "用  检查" {
		t.Fatal("history deletion did not update reference content")
	}
	if v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("history reference changed ordinary draft")
	}
}

func enableReferenceHistory(v *View) {
	m := v.Model
	m.WorkerMinor = 12
	m.Readiness.HarnessID = "test"
	m.Readiness.Harnesses = []domain.HarnessInfo{{ID: "test", Name: "Test", State: "ready", Models: m.Readiness.Models, Permissions: m.Readiness.Permissions, Capabilities: domain.HarnessCapabilities{Restart: true, Fork: true, Skills: true}}}
	d := m.Details[m.SessionID]
	d.Session.HarnessID = "test"
	m.Details[m.SessionID] = d
	for i := range m.Snapshot.Sessions {
		if m.Snapshot.Sessions[i].ID == m.SessionID {
			m.Snapshot.Sessions[i].HarnessID = "test"
		}
	}
}

func TestHistoryPickersEditOwnInlineDraftAndSubmitReferences(t *testing.T) {
	v, ui := referenceComposer(t)
	conn, err := net.Dial("unix", filepath.Join(v.Controller.Directory, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m := v.Model
	cfg := m.Config()
	cfg.Permission = "read-only"
	var accepted domain.Operation
	if err = client.Call(context.Background(), "session.submit", domain.Submission{OperationID: "history-source", WorkspaceID: m.WorkspaceID, Text: "initial", HarnessID: m.HarnessID(), Config: cfg}, &accepted); err != nil {
		t.Fatal(err)
	}
	var detail domain.SessionDetail
	waitNative(t, ui, func() bool {
		if err = client.Call(context.Background(), "session", protocol.SessionQuery{ID: accepted.SessionID}, &detail); err != nil {
			return false
		}
		if detail.Session.Status == "failed" {
			t.Fatal("source turn failed", detail.Session.Error)
		}
		return detail.Session.Status == "completed" && len(detail.Items) > 0 && detail.Items[0].Status == "sent"
	})
	m.SessionID = accepted.SessionID
	detail.HistoryLoaded = true // The fixture completed the legacy session read above.
	m.Details[m.SessionID] = detail
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, detail.Session)
	m.Drafts[m.DraftKey()] = "ordinary draft stays"
	ui.Frame()
	v.beginHistoryEdit(detail.Items[0])
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	ui.Click("编辑历史消息")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("/review")
	waitNative(t, ui, func() bool { return ui.HasText("选择 Skill") && ui.HasText("review-tree") })
	ui.Key(0, n.KeyEnter)
	key := m.HistoryDraftKey()
	waitNative(t, ui, func() bool { return len(m.DraftSkills[key]) == 1 })
	ui.Type("请检查边界条件")
	for range len([]rune("边界条件")) {
		ui.Key(0, n.KeyLeft)
	}
	ui.Type(" @rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(m.Attachments[key]) == 1 })
	if got := m.Drafts[key]; got != " 请检查  边界条件" {
		t.Fatal("history reference consumed the original suffix", got)
	}
	if got := v.composerDocuments[key].doc.Text().String(); got != "\uFFFC 请检查 \uFFFC 边界条件" {
		t.Fatal("history reference did not stay at the insertion point", got)
	}
	if m.Drafts[m.DraftKey()] != "ordinary draft stays" || len(m.DraftSkills[m.DraftKey()]) != 0 || len(m.Attachments[m.DraftKey()]) != 0 {
		t.Fatal("history picker changed ordinary composer")
	}
	ui.Key(n.Cmd, n.KeyEnter)
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	found := false
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "session.restart" {
			change := job.Input.(domain.HistoryChange)
			if change.References == nil || len(change.References.Skills) != 1 || len(change.References.Attachments) != 1 || len(change.References.ReferencePositions) != 2 {
				t.Fatal("history confirmation omitted edited references", change)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("history edit did not dispatch replacement", m.Error, m.HistoryEdits, v.historyExit)
	}
}

func TestSentReferenceNativeCopySurvivesVirtualUnmount(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 120; i++ {
		item := domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Text: fmt.Sprintf("Prompt %03d ", i)}
		if i == 0 {
			item.Skills = []domain.SkillReference{{ID: "check", Name: "check"}}
			item.ReferencePositions = []domain.ReferencePosition{{SkillID: "check", Position: 12}}
		}
		d.Items = append(d.Items, item, domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Text: strings.Repeat("Readable response.\n\n", 3), Final: true})
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	v.lists["s"].ScrollTo(0, n.Start)
	ui.Frame()
	body, ok := ui.Find("用户消息正文")
	if !ok {
		t.Fatal("inline message absent")
	}
	ui.ClickAt(body.X+3, body.Y+5)
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(n.Cmd, n.KeyC)
	if got := ui.Clipboard(); got != "Prompt 000 check" {
		t.Fatal("native selection lost reference name", got)
	}
	v.lists["s"].ScrollToEnd()
	ui.Frame()
	if _, ok := ui.Find("Skill：check"); ok {
		t.Fatal("selected reference stayed mounted")
	}
	ui.SetClipboard("sentinel")
	ui.Key(n.Cmd, n.KeyC)
	if got := ui.Clipboard(); got != "Prompt 000 check" {
		t.Fatal("virtualized selection lost reference name", got)
	}
}

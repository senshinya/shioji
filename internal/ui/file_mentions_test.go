package ui

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"shioji/internal/worker"
)

func referenceComposer(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sh-ref-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	server, err := worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	root := t.TempDir()
	skill := filepath.Join(root, ".agents", "skills", "review-tree")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review-tree\ndescription: Review tree rotations\n---\n\nCheck local tree invariants.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "rotate.go"), []byte("package tree\n"), 0600)
	os.WriteFile(filepath.Join(root, "rotate_test.go"), []byte("package tree\n"), 0600)
	var w domain.Workspace
	if err = client.Call(context.Background(), "workspace.create", protocol.WorkspaceInput{Name: "Tree", Folders: []string{root}}, &w); err != nil {
		t.Fatal(err)
	}
	m := app.NewModel()
	m.Connected = true
	m.WorkerMinor = protocol.Minor
	m.WorkspaceID = w.ID
	m.KnownDeviceID = client.DeviceID
	m.LocalDeviceID = client.DeviceID
	if err = client.Call(context.Background(), "snapshot", nil, &m.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err = client.Call(context.Background(), "readiness", nil, &m.Readiness); err != nil {
		t.Fatal(err)
	}
	v := New(m, app.NewController(dir), filepath.Join(t.TempDir(), "client.json"))
	t.Cleanup(func() { v.Saver.Close() })
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	return v, ui
}
func waitNative(t *testing.T, ui *n.Tester, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ui.Advance(20 * time.Millisecond)
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("native condition not reached", ui.Texts())
}
func TestFileMentionCompletesWithoutSendingAndPreservesDraft(t *testing.T) {
	v, ui := referenceComposer(t)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("请检查 @rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	if !ui.Focused("消息输入") {
		t.Fatal("file search stole composer focus")
	}
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	if v.Model.Drafts["home"] != "请检查 " || len(v.Model.Pending) != 0 {
		t.Fatal("completion submitted or damaged draft", v.Model.Drafts, v.Model.Pending)
	}
	a := v.Model.Attachments["home"][0]
	if !a.Ready || !strings.HasSuffix(a.Attachment.SourcePath, "/rotate.go") {
		t.Fatal("selected reference lost source/ready state", a)
	}
	ui.Advance(400 * time.Millisecond)
	chip, ok := nativeReferenceBox(v, "home", "rotate.go")
	if !ok || chip.H > 30 {
		t.Fatal("reference is not a compact tag", chip)
	}
	ui.Key(0, n.KeyBackspace)
	ui.Advance(400 * time.Millisecond)
	if len(v.Model.Attachments["home"]) != 0 || v.Model.Drafts["home"] != "请检查 " {
		t.Fatal("removal changed ordinary draft")
	}
}
func TestFileMentionDismissAndEmailDoNotOpenOrSend(t *testing.T) {
	_, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("a@rotate.go")
	ui.Advance(400 * time.Millisecond)
	if _, ok := ui.Find("引用工作区文件"); ok {
		t.Fatal("email triggered file picker")
	}
	ui.Type(" @rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	ui.Key(0, n.KeyEscape)
	ui.Advance(400 * time.Millisecond)
	if _, ok := ui.Find("引用工作区文件"); ok {
		t.Fatal("escape reopened same mention")
	}
}

func TestFileMentionKeyboardSelectionPreviewAndUnicodeCaret(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("检查 @rot 后面保留")
	// Move before the suffix using the native rune caret. Completion must replace
	// only the active token, preserving both Chinese text and the suffix.
	ui.Key(0, n.KeyLeft)
	ui.Key(0, n.KeyLeft)
	ui.Key(0, n.KeyLeft)
	ui.Key(0, n.KeyLeft)
	ui.Key(0, n.KeyLeft)
	waitNative(t, ui, func() bool { return ui.HasText("rotate_test.go") })
	ui.Key(0, n.KeyDown)
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	if got := v.Model.Attachments["home"][0].Attachment.Name; got != "rotate_test.go" {
		t.Fatal("arrow selection ignored", got)
	}
	if v.Model.Drafts["home"] != "检查  后面保留" {
		t.Fatal("completion damaged text around caret", v.Model.Drafts["home"])
	}
	ui.Advance(400 * time.Millisecond)
	box, ok := nativeReferenceBox(v, "home", "rotate_test.go")
	if !ok {
		t.Fatal("inline reference is missing")
	}
	ui.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	waitNative(t, ui, func() bool { return ui.HasText("package tree\n") || ui.HasText("package tree") })
	if !ui.HasText("rotate_test.go") {
		t.Fatal("preview lost reference name")
	}
}

func TestSlashSkillMenuUsesStructuredReferenceAndKeepsOrdinaryText(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/review-tree")
	waitNative(t, ui, func() bool { return ui.HasText("review-tree") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 1 })
	if v.Model.Drafts["home"] != "" || len(v.Model.Pending) != 0 {
		t.Fatal("skill selection sent text instead of preparing a reference", v.Model.Drafts)
	}
	if !ui.Focused("消息输入") {
		t.Fatal("skill selection stole typing focus")
	}
	ui.Type("检查旋转")
	in, err := v.Model.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(in)
	if !strings.Contains(string(data), `"skills"`) || strings.Contains(in.Text, "$review-tree") || in.Text != "检查旋转" {
		t.Fatal("native syntax leaked into ordinary prompt", string(data))
	}
}

func TestMentionMenuWorksWithoutRootViewViewport(t *testing.T) {
	v, _ := referenceComposer(t)
	v.viewportWidth = 0 // Secondary panes render conversation/composer directly.
	ui := n.NewTester(func(c *n.Context) { v.pumpUpdates(); v.composer(c, c.Theme(), true) }, 520, 700)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	box, ok := ui.Find("引用工作区文件")
	if !ok || box.W < 300 || box.X < 0 || box.X+box.W > 520 {
		t.Fatal("pane menu did not fit its actual native context", box)
	}
}
func TestRemoteReferenceCompletionDoesNotStealNewFocus(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	v.captureMention(v.fileMention)
	var update app.Update
	select {
	case update = <-v.Controller.Updates:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot response missing")
	}
	ui.Click("搜索")
	ui.Click("搜索会话")
	if !ui.Focused("搜索会话") {
		t.Fatal("test did not move focus")
	}
	update(v.Model)
	ui.Frame()
	if !ui.Focused("搜索会话") || ui.Focused("消息输入") {
		t.Fatal("delayed snapshot stole newer focus")
	}
}

func TestRemoteReferenceCompletionPreservesContinuedTypingCaret(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	v.captureMention(v.fileMention)
	var update app.Update
	select {
	case update = <-v.Controller.Updates:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot missing")
	}
	ui.Type(" 后面")
	update(v.Model)
	ui.Frame()
	ui.Frame()
	ui.Type("X")
	if v.Model.Drafts["home"] != " 后面X" {
		t.Fatal("late completion moved the active typing caret", v.Model.Drafts["home"])
	}
}

func TestInlineReferencesWrapInsideComposer(t *testing.T) {
	v, ui := referenceComposer(t)
	w := v.Model.Snapshot.Workspaces[0]
	v.Model.Drafts["home"] = strings.Repeat("正文 ", 24)
	for _, name := range []string{"first-long-reference.go", "second-long-reference.go"} {
		v.Model.Attachments["home"] = append(v.Model.Attachments["home"], app.DraftAttachment{Attachment: domain.Attachment{ID: domain.NewID(), Name: name, SourcePath: "/device/" + name}, DeviceID: w.DeviceID, WorkspaceID: w.ID, Ready: true})
	}
	ui.Frame()
	ui.Advance(time.Second)
	first, ok := nativeReferenceBox(v, "home", "first-long-reference.go")
	if !ok {
		t.Fatal("first token not laid out", v.composerDocuments["home"].doc.Text().String(), v.Model.Attachments["home"])
	}
	second, ok := nativeReferenceBox(v, "home", "second-long-reference.go")
	if !ok {
		t.Fatal("second token not laid out")
	}
	input, _ := ui.Find("消息输入")
	if first.Y < input.Y || second.Y < input.Y || second.Y+second.H > input.Y+input.H || first.X < input.X || second.X+second.W > input.X+input.W+1 {
		t.Fatal("references did not wrap within native input", first, second, input)
	}
}

// Read the same native shaped object rectangles used for caret placement.
func nativeReferenceBox(v *View, key, name string) (n.Rect, bool) {
	b := v.composerDocuments[key]
	if b == nil {
		return n.Rect{}, false
	}
	text := b.doc.Text()
	spans := []n.Span{}
	indices := map[int]string{}
	for at, run := range text.Runs(0, text.Len()) {
		span := n.Span{Text: run.Text}
		if a := run.Attributes.Attachment; a != nil {
			span.ObjectWidth = a.Width
			span.ObjectHeight = a.Height - a.Descent
			span.ObjectDescent = a.Descent
			ref, ok := a.Data.(composerReference)
			if ok {
				indices[at] = ref.name()
			}
		}
		spans = append(spans, span)
	}
	layout := n.ShapeRichTextWith(spans, n.Font{Size: 15, Weight: 400}, n.TextShape{Width: v.inputBounds.W, LineHeight: 26})
	for _, object := range layout.Objects() {
		// These fixtures use BMP text; object indices are UTF-16 offsets.
		if indices[object.Index] == name {
			r := object.Rect
			r.X += v.inputBounds.X
			r.Y += v.inputBounds.Y
			return r, true
		}
	}
	return n.Rect{}, false
}

func TestMentionSearchUpdatesKeepPopoverPresent(t *testing.T) {
	for _, trigger := range []string{"@rot", "/review"} {
		t.Run(trigger, func(t *testing.T) {
			v, ui := referenceComposer(t)
			ui.Click("消息输入")
			ui.Type(trigger)
			waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
			ui.Advance(300 * time.Millisecond)
			title := v.fileMention.title()
			before, ok := ui.Find(title)
			if !ok {
				t.Fatal("picker not open")
			}
			inputBefore, _ := ui.Find("消息输入")
			ui.Type("e")
			for frame := 0; frame < 12; frame++ {
				panel, ok := ui.Find(title)
				if !ok || panel.W < before.W-1 {
					t.Fatal("search update closes or replays popup entrance", frame, panel, ok, before)
				}
				input, _ := ui.Find("消息输入")
				if input.Y != inputBefore.Y || !ui.Focused("消息输入") {
					t.Fatal("lookup moved or unfocused composer")
				}
				ui.Advance(16 * time.Millisecond)
			}
		})
	}
}

func TestReferenceIsAtomicInlineTokenWithUndo(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("检查 @rot 后续")
	for range len([]rune(" 后续")) {
		ui.Key(0, n.KeyLeft)
	}
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	ui.Type("继续")
	if v.Model.Drafts["home"] != "检查 继续 后续" {
		t.Fatal("typing did not continue at insertion point", v.Model.Drafts["home"])
	}
	ui.Key(0, n.KeyBackspace)
	ui.Key(0, n.KeyBackspace)
	ui.Key(0, n.KeyBackspace)
	if len(v.Model.Attachments["home"]) != 0 || v.Model.Drafts["home"] != "检查  后续" {
		t.Fatal("backspace did not delete exactly the inline token", v.Model.Drafts["home"], v.Model.Attachments["home"])
	}
	ui.Key(n.Cmd, n.KeyZ)
	if len(v.Model.Attachments["home"]) != 1 {
		t.Fatal("undo did not restore reference metadata")
	}
	ui.Key(n.Cmd|n.Shift, n.KeyZ)
	if len(v.Model.Attachments["home"]) != 0 {
		t.Fatal("redo did not remove reference")
	}
}

func TestInlineReferenceCopyIncludesVisibleName(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/review-tree")
	waitNative(t, ui, func() bool { return ui.HasText("review-tree") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 1 })
	ui.Type(" 检查")
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(n.Cmd, n.KeyC)
	if got := ui.Clipboard(); got != "review-tree 检查" {
		t.Fatal("copy lost the inline name", got)
	}
}

func TestInlineReferenceRestoresPositionAfterReload(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("检查 @rot 后续")
	for range len([]rune(" 后续")) {
		ui.Key(0, n.KeyLeft)
	}
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	if err := v.Flush(); err != nil {
		t.Fatal(err)
	}
	m := app.NewModel()
	if err := m.Load(v.StatePath); err != nil {
		t.Fatal(err)
	}
	m.Snapshot = v.Model.Snapshot
	fresh := New(m, nil, "")
	restored := n.NewTester(func(c *n.Context) { fresh.composer(c, c.Theme(), true) }, 760, 500)
	b := fresh.composerDocuments["home"]
	if got := b.doc.Text().String(); got != "检查 \uFFFC 后续" {
		t.Fatal("reload moved the inline token", got)
	}
	if err := restored.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	b.doc.SetSelection(4, 4)
	restored.Frame()
	restored.Key(0, n.KeyBackspace)
	if len(m.Attachments["home"]) != 0 || m.Drafts["home"] != "检查  后续" {
		t.Fatal("restored reference lost atomic editing", m.Drafts["home"], m.Attachments["home"])
	}
}

func TestCapturedReferenceRevalidatesCombinedAttachmentLimit(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	v.captureMention(v.fileMention)
	var update app.Update
	select {
	case update = <-v.Controller.Updates:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot missing")
	}
	w := v.Model.Snapshot.Workspaces[0]
	for range 8 {
		v.Model.Attachments["home"] = append(v.Model.Attachments["home"], app.DraftAttachment{Attachment: domain.Attachment{ID: domain.NewID(), Name: "file.txt", MIME: "text/plain", Size: 1, SHA256: strings.Repeat("0", 64)}, WorkspaceID: w.ID, DeviceID: w.DeviceID, Ready: true})
	}
	update(v.Model)
	ui.Frame()
	if len(v.Model.Attachments["home"]) != 8 || v.Model.Drafts["home"] != "@rot" {
		t.Fatal("late reference bypassed attachment limit", len(v.Model.Attachments["home"]), v.Model.Drafts["home"])
	}
}

func TestMentionIMEDoesNotDismissOrSubmit(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	ui.Advance(300 * time.Millisecond)
	ui.Compose("树", 1)
	if _, ok := ui.Find("引用工作区文件"); !ok {
		t.Fatal("IME composition dismissed picker")
	}
	ui.Key(0, n.KeyEnter)
	if len(v.Model.Pending) != 0 || len(v.Model.Attachments["home"]) != 0 {
		t.Fatal("IME confirmation selected a reference or sent message")
	}
}

func TestDismissedFileCaptureCannotReappearOrInsert(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("@rot")
	waitNative(t, ui, func() bool { return v.fileMention != nil && v.fileMention.phase == mentionShowing })
	v.captureMention(v.fileMention)
	var update app.Update
	select {
	case update = <-v.Controller.Updates:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot missing")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(200 * time.Millisecond)
	update(v.Model)
	ui.Frame()
	if len(v.Model.Attachments["home"]) != 0 || v.Model.Drafts["home"] != "@rot" {
		t.Fatal("dismissed capture inserted later", v.Model.Drafts["home"], v.Model.Attachments["home"])
	}
}

func TestOrdinaryAttachmentUpdateKeepsNativeCaretAndUndo(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Type("before after")
	for range len(" after") {
		ui.Key(0, n.KeyLeft)
	}
	ui.Type("X")
	v.Model.Attachments["session:s"] = []app.DraftAttachment{{Attachment: domain.Attachment{ID: "image", Name: "image.png", MIME: "image/png"}, Ready: true}}
	ui.Frame()
	v.Model.Attachments["session:s"][0].Ready = false
	v.Model.Attachments["session:s"][0].Error = "upload interrupted"
	ui.Frame()
	ui.Type("Y")
	if got := v.Model.Drafts["session:s"]; got != "beforeXY after" {
		t.Fatal("attachment metadata reset caret", got)
	}
	ui.Key(n.Cmd, n.KeyZ)
	if got := v.Model.Drafts["session:s"]; got != "before after" && got != "beforeX after" {
		t.Fatal("attachment metadata cleared undo", got)
	}
}

func TestQueuedReferencesReturnToOriginalInlinePositions(t *testing.T) {
	v, ui := referenceComposer(t)
	ui.Click("消息输入")
	ui.Type("/review-tree")
	waitNative(t, ui, func() bool { return ui.HasText("review-tree") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 1 })
	ui.Type("检查 @rot 后续")
	for range len([]rune(" 后续")) {
		ui.Key(0, n.KeyLeft)
	}
	waitNative(t, ui, func() bool { return ui.HasText("rotate.go") })
	ui.Key(0, n.KeyEnter)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	want := v.composerDocuments["home"].doc.Text().String()
	in, err := v.Model.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(in)
	if !strings.Contains(string(body), `"referencePositions"`) {
		t.Fatal("accepted input dropped token positions")
	}
	var transferred domain.Submission
	if err = json.Unmarshal(body, &transferred); err != nil {
		t.Fatal(err)
	}
	m := v.Model
	sid := "queued-inline"
	session := domain.Session{ID: sid, WorkspaceID: m.WorkspaceID, HarnessID: m.HarnessID(), Status: "running"}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, session)
	m.SessionID = sid
	transferred.SessionID = sid
	m.Details[sid] = domain.SessionDetail{Session: session, Queue: domain.QueueState{SessionID: sid, Revision: 1, Items: []domain.QueueItem{{ID: "one", Input: transferred}}}}
	clear(m.Pending)
	if err = m.EditQueued("one"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	b := v.composerDocuments["session:"+sid+":edit:"+m.QueueEdits[sid].Token]
	if b == nil || b.doc.Text().String() != want {
		t.Fatal("queue editing moved inline references", want, b)
	}
	m.CancelQueueEdit(sid)
	ui.Frame()
	if m.Drafts["session:"+sid] != "" || len(m.Attachments["session:"+sid]) != 0 || len(m.DraftSkills["session:"+sid]) != 0 {
		t.Fatal("queue edit leaked references into displaced draft")
	}
}

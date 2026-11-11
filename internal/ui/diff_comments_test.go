package ui

import (
	"context"
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestDiffCompareNamesExchangeWithoutMovingControls(t *testing.T) {
	v := &View{Model: app.NewModel()}
	v.Model.WorkerMinor = 33
	s := v.changesFor("s")
	s.Comparison = domain.DiffComparison{Base: domain.DiffEndpoint{Kind: "branch", Name: "main"}, Target: domain.DiffEndpoint{Kind: "branch", Name: "feature"}}
	tester := n.NewTester(func(c *n.Context) { v.diffComparisonBar(c, c.Theme(), s, 420) }, 420, 100)
	a, ok := tester.Find("比较名称:main")
	if !ok {
		t.Fatal("base name absent")
	}
	control, _ := tester.Find("交换比较方向")
	s.Comparison.Base, s.Comparison.Target = s.Comparison.Target, s.Comparison.Base
	tester.Frame()
	tester.Advance(100 * time.Millisecond)
	mid, _ := tester.Find("比较名称:main")
	fixed, _ := tester.Find("交换比较方向")
	if mid.X <= a.X || fixed != control {
		t.Fatal("names did not move independently", a, mid, control, fixed)
	}
	tester.Advance(time.Second)
	end, _ := tester.Find("比较名称:main")
	if mid.X >= end.X {
		t.Fatal("swap jumped to final position", mid, end)
	}
	tester.SetPreferences(n.Preferences{ReduceMotion: true})
	s.Comparison.Base, s.Comparison.Target = s.Comparison.Target, s.Comparison.Base
	tester.Frame()
	back, _ := tester.Find("比较名称:main")
	if back.X != a.X {
		t.Fatal("reduced motion swap not immediate", back, a)
	}
}
func TestCommentResourceRendersInlineAndCopiesItsLabel(t *testing.T) {
	v, ui, item := historyFixture()
	enableReferenceHistory(v)
	item.Text = "检查 。"
	item.Attachments = []domain.Attachment{{ID: "comment", Kind: domain.DiffCommentResource, Name: "rotation.go:13", MIME: "text/plain", Size: 10}}
	item.ReferencePositions = []domain.ReferencePosition{{AttachmentID: "comment", Position: 4}}
	d := v.Model.Details["s"]
	d.Items[0] = item
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("预览引用：rotation.go:13"); !ok {
		t.Fatal("comment is not an inline tag")
	}
	ui.Click("复制消息")
	if ui.Clipboard() != "检查 rotation.go:13。" {
		t.Fatal("copy lost comment tag", ui.Clipboard())
	}
}

func TestComparisonEmptyFileRetainsHeaderAndShowsExplicitState(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Path = "main.go"
	state.installDiff(domain.FileVersions{Path: "main.go", Unchanged: true, Comparison: state.Comparison})
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if !ui.HasText("main.go") || !ui.HasText("当前文件无差异") {
		t.Fatal("file disappeared instead of showing no diff", ui.Texts())
	}
	if _, ok := ui.Find("返回更改列表"); !ok {
		t.Fatal("no-diff file lost navigation")
	}
}

func TestNativeCommentInsertionPreservesDraftAndAddsPreciseReference(t *testing.T) {
	v, ui := referenceComposer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go v.Controller.Run(ctx)
	waitNative(t, ui, func() bool { return v.Model.ConnectionEpoch != "" })
	// Use the same real owning worker, with an isolated Git repository.
	root := v.Model.Snapshot.Workspaces[0].Folders[0]
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.invalid")
	run("add", ".")
	run("commit", "-m", "initial")
	os.WriteFile(filepath.Join(root, "rotate.go"), []byte("package tree\nroot.Right = next.Left\n"), 0600)
	state := v.changesFor("")
	state.Path = "rotate.go"
	v.requestComparison(state, domain.DefaultDiffComparison())
	waitNative(t, ui, func() bool { return state.fileAvailable() && state.Versions.Snapshot.ID != "" })
	binding := v.composerDocuments["home"]
	v.Model.Drafts["home"] = "keep ordinary text"
	ui.Frame()
	binding = v.composerDocuments["home"]
	binding.doc.SetSelection(5, 5)
	state.Diff.SetTextSelection(editor.DiffTextPoint{Side: editor.DiffNew, Offset: 26}, editor.DiffTextPoint{Side: editor.DiffNew, Offset: 35})
	selection, ok := state.Diff.SelectedText()
	if !ok || selection.Text != "next.Left" {
		t.Fatal(selection)
	}
	draft := &diffCommentDraft{Selection: selection, Snapshot: state.Versions.Snapshot, Versions: state.Versions, Text: "检查空值", Open: true}
	// The actual acceptance callback enqueues insertion into a fresh native
	// frame, never editing a document under its own Edit lock.
	v.saveDiffComment(nil, state, draft)
	waitNative(t, ui, func() bool { return len(v.Model.Attachments["home"]) == 1 })
	if v.Model.Drafts["home"] != "keep ordinary text" {
		t.Fatal("comment consumed ordinary input", v.Model.Drafts["home"])
	}
	if got := v.Model.DraftReferencePositions("home"); len(got) != 1 || got[0].Position != 6 {
		t.Fatal("comment did not enter at caret", got)
	}
	if v.Model.Attachments["home"][0].Attachment.Kind != domain.DiffCommentResource {
		t.Fatal("comment became an ordinary file")
	}
	first := v.Model.Attachments["home"][0]
	if len(state.CommentNotes) != 1 || state.CommentNotes[0].Draft != draft {
		t.Fatalf("new comment remounted after save: notes=%+v draft=%p state=%p actual=%p session=%q", state.CommentNotes, draft, state, v.changesFor(v.Model.SessionID), v.Model.SessionID)
	}
	stableKey := draft.UIKey
	v.editDiffComment(state, first, v.commentResources[v.commentResourceKey(first)].Record)
	draft.Text = "修订后的检查"
	v.saveDiffComment(nil, state, draft)
	waitNative(t, ui, func() bool {
		return len(v.Model.Attachments["home"]) == 1 && v.Model.Attachments["home"][0].Attachment.ID != first.Attachment.ID
	})
	if len(state.CommentNotes) != 1 || state.CommentNotes[0].Draft != draft || draft.UIKey != stableKey || draft.Open || draft.Text != "修订后的检查" {
		t.Fatal("resource replacement lost shared bubble", state.CommentNotes)
	}
	if v.Model.Drafts["home"] != "keep ordinary text" {
		t.Fatal("edit consumed independent composer")
	}
}

func TestPreciseCommentOpensWithActualHeightAnimation(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Path = "main.go"
	state.installDiff(domain.FileVersions{Path: "main.go", Old: "old\n", New: "root.Right = next.Left\n", Comparison: state.Comparison, Snapshot: domain.Attachment{ID: "snapshot", Kind: domain.DiffSnapshotResource}})
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	state.Diff.SetTextSelection(editor.DiffTextPoint{Side: editor.DiffNew, Offset: 13}, editor.DiffTextPoint{Side: editor.DiffNew, Offset: 22})
	ui.Frame()
	if err := ui.Click("添加代码评论"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(80 * time.Millisecond)
	if len(state.Diff.Annotations) == 0 || state.Diff.Annotations[0].Height <= 0 || state.Diff.Annotations[0].Height >= 188 {
		t.Fatal("editor did not animate actual height", state.Diff.Annotations)
	}
	ui.Advance(time.Second)
	if _, ok := ui.Find("代码评论内容"); !ok {
		t.Fatal("editor input absent")
	}
	panel, _ := ui.Find("代码评论编辑器")
	viewer, _ := ui.Find("原生文件差异")
	if panel.X+panel.W > viewer.X+viewer.W-8 {
		t.Fatal("comment editor extends beyond Diff edge", panel, viewer)
	}
	ui.Click("取消评论")
	ui.Advance(80 * time.Millisecond)
	if len(state.Diff.Annotations) == 0 || state.Diff.Annotations[0].Height <= 0 || state.Diff.Annotations[0].Height >= 188 {
		t.Fatal("editor close did not animate height", state.Diff.Annotations)
	}
	ui.Advance(time.Second)
	if len(state.Diff.Annotations) != 0 {
		t.Fatal("closed editor still reserves height")
	}
}

func TestComparisonLateResultCannotReplaceNewerPairAndFailureKeepsFile(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	m.Connected = true
	m.ConnectionEpoch = "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Path = "file.go"
	state.installDiff(domain.FileVersions{Path: "file.go", Old: "old", New: "new"})
	first := domain.DefaultDiffComparison()
	second := domain.DiffComparison{Base: domain.DiffEndpoint{Kind: "branch", Name: "main"}, Target: domain.DiffEndpoint{Kind: "branch", Name: "feature"}}
	v.requestComparison(state, first)
	old := <-controller.Jobs
	v.requestComparison(state, second)
	latest := <-controller.Jobs
	body, _ := json.Marshal(domain.ComparisonResult{Comparison: second, Versions: &domain.FileVersions{Path: "file.go", Unchanged: true, Comparison: second}})
	latest.Apply(m, body, nil)
	old.Apply(m, nil, fmt.Errorf("late failure"))
	if state.Comparison != second || state.Error != "" || !state.Versions.Unchanged {
		t.Fatal("late comparison changed accepted content", state)
	}
	v.requestComparison(state, first)
	failed := <-controller.Jobs
	failed.Apply(m, nil, fmt.Errorf("could not read"))
	if state.Comparison != second || !state.fileAvailable() || state.FailedComparison == nil || *state.FailedComparison != first {
		t.Fatal("failed switch replaced file or lost retry target", state)
	}
}

func TestComparisonReturnRetainsOpenCommentFileSnapshot(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	m.Connected = true
	m.ConnectionEpoch = "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Path = "file.go"
	pair := domain.DefaultDiffComparison()
	pair.Repository = "repo"
	state.Comparison = pair
	captured := domain.FileVersions{Path: "file.go", Old: "old", New: "selected version", Comparison: pair, Snapshot: domain.Attachment{ID: "captured", SHA256: "captured"}}
	state.installDiff(captured)
	state.CommentDrafts = map[diffCommentKey]*diffCommentDraft{{pair, "file.go"}: {Open: true, Text: "keep comment", Snapshot: captured.Snapshot, Versions: captured}}
	v.requestComparison(state, pair)
	job := <-controller.Jobs
	body, _ := json.Marshal(domain.ComparisonResult{Comparison: pair, Versions: &domain.FileVersions{Path: "file.go", Old: "old", New: "new working copy", Comparison: pair, Snapshot: domain.Attachment{ID: "new", SHA256: "new"}}})
	job.Apply(m, body, nil)
	if state.Versions.New != "selected version" || !state.PinnedSnapshot || state.commentDraft().Text != "keep comment" {
		t.Fatal("open comment was attached to a changed file", state.Versions)
	}
}

func TestNewCommentTagFadesWithoutChangingItsBox(t *testing.T) {
	var born time.Time
	ui := n.NewTester(func(c *n.Context) {
		if born.IsZero() {
			born = c.Now()
		}
		tag := inlineTagAttachment(c, "rotation.go:13", diffCommentIcon, 14, born)
		n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: tag.Width, ObjectHeight: tag.Height - tag.Descent, ObjectDescent: tag.Descent}).FontSize(14).FixedLineHeight(24).Label("tag").DrawObjects(func(p *n.Painter, _ int, r n.Rect) { tag.Draw(p, r) })
	}, 320, 80)
	box, _ := ui.Find("tag")
	rest := ui.Image().RGBAAt(int(box.X+8), int(box.Y+8))
	ui.Advance(80 * time.Millisecond)
	middle := ui.Image().RGBAAt(int(box.X+8), int(box.Y+8))
	ui.Advance(300 * time.Millisecond)
	end := ui.Image().RGBAAt(int(box.X+8), int(box.Y+8))
	after, _ := ui.Find("tag")
	if rest == middle || middle == end || box != after {
		t.Fatal("tag did not fade within a stable box", rest, middle, end, box, after)
	}
}

func TestCommentGlyphHasTopBreathingRoomAtEveryUsedSize(t *testing.T) {
	for _, size := range []float32{13, 14, 16} {
		for _, scale := range []float32{1, 2} {
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Draw(func(p *n.Painter, _ n.Rect) {
					p.Icon(diffCommentIcon, n.Rect{X: 8, Y: 8, W: size, H: size}, c.Theme().Text)
				})
			}, 40, 40)
			ui.SetScale(scale)
			image := ui.Image()
			background := image.RGBAAt(0, 0)
			top := image.Bounds().Max.Y
			for y := int(8 * scale); y < int((8+size)*scale); y++ {
				for x := int(8 * scale); x < int((8+size)*scale); x++ {
					pixel := image.RGBAAt(x, y)
					if abs(int(pixel.R)-int(background.R)) > 40 {
						top = min(top, y)
					}
				}
			}
			if top <= int(8*scale) {
				t.Fatalf("comment outline touches/clips the SVG top at size %g scale %g", size, scale)
			}
		}
	}
}

func TestDiffToEmptyKeepsOutgoingRendererAliveDuringFade(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Path = "file.go"
	state.installDiff(domain.FileVersions{Path: "file.go", Old: "old\n", New: "new\n", Comparison: state.Comparison})
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	next := state.Comparison
	next.Target = domain.DiffEndpoint{Kind: "branch", Name: "same"}
	state.Comparison = next
	state.installDiff(domain.FileVersions{Path: "file.go", Unchanged: true, Comparison: next})
	ui.Frame()
	ui.Advance(80 * time.Millisecond)
	if !ui.HasText("当前文件无差异") {
		t.Fatal("missing incoming empty state")
	}
	ui.Advance(time.Second)
}

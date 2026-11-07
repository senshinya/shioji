package ui

import (
	"image"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestParallelOpeningAndClosingResizeTheExistingConversation(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := parallelModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1600, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		before, _ := ui.Find("对话记录")
		v.openParallel("other")
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		middle, _ := ui.Find("对话记录")
		ui.Advance(300 * time.Millisecond)
		after, _ := ui.Find("对话记录")
		if !reduced && !(before.W > middle.W && middle.W > after.W) {
			t.Fatalf("opening split jumps the original conversation: %v -> %v -> %v", before, middle, after)
		}
		if reduced && middle.W != after.W {
			t.Fatal("Reduce Motion interpolates split geometry")
		}
		v.closeParallel()
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		closing, _ := ui.Find("对话记录")
		ui.Advance(300 * time.Millisecond)
		closed, _ := ui.Find("对话记录")
		if !reduced && !(after.W < closing.W && closing.W < closed.W) {
			t.Fatalf("closing split jumps the original conversation: %v -> %v -> %v", after, closing, closed)
		}
		if math.Abs(float64(closed.W-before.W)) > 1 || m.Drafts["session:other"] != "right draft" {
			t.Fatal("closing split changed geometry or lost draft")
		}
	}
}

func TestRemainingAttachmentMovesIntoTheRemovedSlot(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 6
	for _, name := range []string{"first.go", "second.go"} {
		m.Attachments["session:s"] = append(m.Attachments["session:s"], app.DraftAttachment{Attachment: domain.Attachment{ID: domain.NewID(), Name: name, MIME: "text/plain", Size: 10, SHA256: strings.Repeat("a", 64)}, WorkspaceID: "w", Ready: true})
	}
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("预览附件：second.go")
	ui.Click("移除附件：first.go")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("预览附件：second.go")
	ui.Advance(400 * time.Millisecond)
	after, _ := ui.Find("预览附件：second.go")
	if !(before.X > middle.X && middle.X > after.X) {
		t.Fatal("remaining attachment jumps after removal", before, middle, after)
	}
}

func TestParallelReversalKeepsTheDividerAndOriginalList(t *testing.T) {
	m := parallelModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1600, 900)
	ui.Advance(time.Second)
	list := v.lists["s"]
	v.openParallel("other")
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	left, _ := ui.Find("左侧会话")
	right, _ := ui.Find("右侧会话")
	if math.Abs(float64(left.X+left.W-right.X)) > 1 {
		t.Fatal("animated panes overlap or leave a gap", left, right)
	}
	for _, open := range []bool{false, true, false} {
		ui.Frame()
		before, _ := ui.Find("对话记录")
		if open {
			v.openParallel("other")
		} else {
			v.closeParallel()
		}
		ui.Frame()
		after, _ := ui.Find("对话记录")
		if math.Abs(float64(before.W-after.W)) > 1 {
			t.Fatal("reversing the split jumps the left pane", before, after)
		}
		ui.Advance(50 * time.Millisecond)
	}
	ui.Advance(300 * time.Millisecond)
	if v.lists["s"] != list || ui.HasText("关闭并排会话") {
		t.Fatal("split rebuilds the original list or keeps closed controls")
	}
}

func TestAttachmentStripReversesWithoutMovingTheInputOrLosingFocus(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		ui.Click("消息输入")
		before, _ := ui.Find("消息编辑器")
		input, _ := ui.Find("消息输入")
		a := app.DraftAttachment{Attachment: domain.Attachment{ID: domain.NewID(), Name: "source.go", MIME: "text/plain"}, WorkspaceID: "w", Ready: true}
		m.Attachments["session:s"] = []app.DraftAttachment{a}
		ui.Frame()
		ui.Advance(100 * time.Millisecond)
		ui.Frame()
		middle, _ := ui.Find("消息编辑器")
		if !reduced && !(middle.H > before.H && middle.H < before.H+80) {
			t.Fatal("attachment strip does not push the composer up continuously", before, middle)
		}
		m.Attachments["session:s"] = nil
		ui.Frame()
		reversed, _ := ui.Find("消息编辑器")
		if !reduced && math.Abs(float64(reversed.H-middle.H)) > 1 {
			t.Fatal("attachment strip reversal jumps in height", middle, reversed)
		}
		ui.Advance(500 * time.Millisecond)
		after, _ := ui.Find("消息编辑器")
		afterInput, _ := ui.Find("消息输入")
		if before != after || input != afterInput || !ui.Focused("消息输入") || ui.HasText("移除附件：source.go") {
			t.Fatal("strip exit changed the draft layout, focus, or left an interactive ghost", before, after, input, afterInput)
		}
	}
}

func TestWorkspaceDirectoryRemovalMovesFollowingRowsAndActionsTogether(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Workspaces[0].Folders = []string{"/a", "/b", "/c"}
	v := New(m, nil, "")
	v.editWorkspace(m.Snapshot.Workspaces[0])
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("移除目录：/c")
	footer, _ := ui.Find("取消工作区编辑")
	ui.Click("移除目录：/a")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("移除目录：/c")
	middleFooter, _ := ui.Find("取消工作区编辑")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("移除目录：/c")
	afterFooter, _ := ui.Find("取消工作区编辑")
	if !(before.Y > middle.Y && middle.Y > after.Y) || !(footer.Y > middleFooter.Y && middleFooter.Y > afterFooter.Y) {
		t.Fatal("directory removal jumps rows or footer", before, middle, after, footer, middleFooter, afterFooter)
	}
	if math.Abs(float64((before.Y-middle.Y)-(footer.Y-middleFooter.Y))) > 1 {
		t.Fatal("directory and footer animations use different progress")
	}
}

func TestAttachmentPreviewGrowsWhenTheImageFinishesLoading(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "image", Name: "image.png"}, presence: previewOpening, Loading: true}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("关闭附件预览")
	v.attachmentPreview.Loading = false
	v.attachmentPreview.Image = n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 80, 60)))
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	middle, _ := ui.Find("关闭附件预览")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("关闭附件预览")
	if !(before.Y > middle.Y && middle.Y > after.Y) {
		t.Fatal("loaded preview jumps to its final height", before, middle, after)
	}
}

func TestRenameBackdropFadesOnOpenAndClose(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	rest := ui.Image().RGBAAt(1300, 500)
	v.rename = &sessionRename{Session: domain.Session{ID: "s"}, Title: "title", Open: true}
	ui.Frame()
	ui.Advance(35 * time.Millisecond)
	entering := ui.Image().RGBAAt(1300, 500)
	ui.Advance(200 * time.Millisecond)
	open := ui.Image().RGBAAt(1300, 500)
	if entering == rest || entering == open {
		t.Fatal("modal backdrop appears instantly")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(35 * time.Millisecond)
	exiting := ui.Image().RGBAAt(1300, 500)
	ui.Advance(200 * time.Millisecond)
	closed := ui.Image().RGBAAt(1300, 500)
	if exiting == open || exiting == closed || closed != rest {
		t.Fatal("modal backdrop disappears instantly or remains dimmed", rest, open, exiting, closed)
	}
}

func TestRenameConflictResizesItsDialogContinuously(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.rename = &sessionRename{Session: domain.Session{ID: "s"}, Title: "title", Open: true}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("新的会话名称")
	v.rename.Error = "其他客户端更新了名称，请重新载入后再保存。"
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	middle, _ := ui.Find("新的会话名称")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("新的会话名称")
	if !(before.Y > middle.Y && middle.Y > after.Y) {
		t.Fatal("rename conflict jumps the dialog height", before, middle, after)
	}
}

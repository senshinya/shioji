package ui

import (
	"encoding/json"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestWelcomeLocalAdditionalDirectoryHasPicker(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.directories = []welcomeDirectory{{id: 1, path: "/Users/local/extra"}}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("选择附加目录"); !ok {
		t.Fatal("additional directory has no native folder picker", ui.Texts())
	}
}

func TestDirectoryPickerFadesWithoutMovingOrResizing(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("选择主目录")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("远程文件夹选择器")
	minimum := func(r n.Rect) uint8 {
		im := ui.Image()
		low := uint8(255)
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				low = min(low, im.RGBAAt(x, y).R)
			}
		}
		return low
	}
	textMid, _ := ui.Find("正在读取")
	inkMid := minimum(textMid)
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("远程文件夹选择器")
	textEnd, _ := ui.Find("正在读取")
	inkEnd := minimum(textEnd)
	if mid != end || inkMid <= inkEnd+5 {
		t.Fatal("picker should fade in at its final bounds", mid, end, inkMid, inkEnd)
	}
	ui.Click("取消选择目录")
	ui.Advance(60 * time.Millisecond)
	// Closing content is inert and intentionally absent from accessibility;
	// sample the retained glyphs at their settled coordinates instead.
	inkOut := minimum(textEnd)
	if inkOut <= inkEnd+5 || inkOut >= 250 {
		t.Fatal("picker should fade out at its settled coordinates", inkOut, inkEnd)
	}
	ui.Advance(200 * time.Millisecond)
	if _, ok := ui.Find("远程文件夹选择器"); ok {
		t.Fatal("closed picker remains in the layout")
	}
}

func TestDirectoryPickerHeaderAndSelectionMatchNativeGeometry(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	f.directory = "/Users/remote/tree"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("选择主目录")
	job := welcomeJob(t, c, "device.directories")
	job.Apply(m, json.RawMessage(`{"path":"/Users/remote","parent":"/Users","home":"/Users/remote","entries":[{"name":"tree","path":"/Users/remote/tree"}]}`), nil)
	v.directoryPicker.title = "目录选择"
	ui.Frame()
	ui.Advance(time.Second)
	dialog, _ := ui.Find("远程文件夹选择器")
	title, _ := ui.Find("目录选择")
	if math.Abs(float64(title.Y+title.H/2-(dialog.Y+28))) > 1.5 {
		t.Fatal("directory title is not centered in its header", dialog, title)
	}
	row, _ := ui.Find("文件夹：tree")
	px := ui.Image().RGBAAt(int(row.X), int(row.Y))
	if px.R < 245 {
		t.Fatal("selected row lacks the approved rounded inset", row, px)
	}
}

func TestLocalDirectoryPickerAddsMultipleAndCancelPreservesDraft(t *testing.T) {
	previous := openWorkspaceDirectoryDialog
	t.Cleanup(func() { openWorkspaceDirectoryDialog = previous })
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.directory = "/local/main"
	openWorkspaceDirectoryDialog = func(opts mygo.OpenDialogOptions) ([]string, error) {
		if !opts.Directory || !opts.Multiple {
			t.Fatal(opts)
		}
		return []string{"/local/main", "/local/one", "/local/two"}, nil
	}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("添加文件夹")
	ui.Advance(time.Second)
	if len(f.directories) != 2 || f.directories[0].path != "/local/one" || f.directories[1].path != "/local/two" {
		t.Fatal(f.directories)
	}
	openWorkspaceDirectoryDialog = func(mygo.OpenDialogOptions) ([]string, error) { return nil, nil }
	ui.Click("选择附加目录")
	ui.Frame()
	if len(f.directories) != 2 || f.directories[0].path != "/local/one" {
		t.Fatal("cancel altered directory drafts", f.directories)
	}
}

func TestDirectoryPickerRejectsStaleQueryAndConnection(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	f.directory = "/Users/remote/old"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("选择主目录")
	old := welcomeJob(t, c, "device.directories")
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("远程目录路径")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("/Users/remote/new")
	ui.Key(0, n.KeyEnter)
	newer := welcomeJob(t, c, "device.directories")
	if newer.Input.(protocol.DirectoryQuery).Path != "/Users/remote/new" {
		t.Fatal("Enter did not use current native input text", newer.Input)
	}
	newer.Apply(m, json.RawMessage(`{"path":"/Users/remote/new","parent":"/Users/remote","home":"/Users/remote","entries":[]}`), nil)
	old.Apply(m, json.RawMessage(`{"path":"/Users/remote/old","parent":"/Users/remote","home":"/Users/remote","entries":[{"name":"stale","path":"/Users/remote/old/stale"}]}`), nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("stale") || !ui.HasText("此文件夹为空") {
		t.Fatal("stale directory response replaced current page", ui.Texts())
	}
	m.DeviceStates["air"].ConnectionEpoch = "replacement"
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("选择文件夹")
	ui.Frame()
	if f.directory != "/Users/remote/old" {
		t.Fatal("changed connection could apply directory", f.directory)
	}
}
func TestWelcomeRemoteDirectoryPickerUsesOwningWorkerAndPreservesCancel(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	f.directory = "/Users/remote/old"
	f.name = "Remote"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("选择主目录"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, c, "device.directories")
	if job.ConnectionID != "air" || job.ConnectionEpoch != "b" {
		t.Fatal("directory request sent to wrong device", job)
	}
	body := json.RawMessage(`{"path":"/Users/remote","parent":"/Users","home":"/Users/remote","entries":[{"name":"tree","path":"/Users/remote/tree"}]}`)
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("tree") {
		t.Fatal(ui.Texts())
	}
	if err := ui.Click("文件夹：tree"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("选择文件夹"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if f.directory != "/Users/remote/tree" {
		t.Fatal("remote selection not returned to draft", f.directory)
	}
	ui.Click("选择主目录")
	job = welcomeJob(t, c, "device.directories")
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("取消选择目录")
	ui.Frame()
	ui.Advance(time.Second)
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("tree") || f.directory != "/Users/remote/tree" {
		t.Fatal("late read reopened the cancelled picker", ui.Texts(), f.directory)
	}
}

func TestFirstDirectoryResultDoesNotSlideButNavigationDoes(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	ctrl := app.NewController("")
	v := New(m, ctrl, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("选择主目录")
	ui.Advance(300 * time.Millisecond)
	job := welcomeJob(t, ctrl, "device.directories")
	job.Apply(m, json.RawMessage(`{"path":"/Users/remote","home":"/Users/remote","entries":[{"name":"tree","path":"/Users/remote/tree"}]}`), nil)
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	first, _ := ui.Find("文件夹：tree")
	ui.Advance(300 * time.Millisecond)
	settled, _ := ui.Find("文件夹：tree")
	if first.X != settled.X {
		t.Fatal("initial result slides into a newly opened picker", first, settled)
	}
	ui.Click("文件夹：tree")
	ui.Advance(80 * time.Millisecond)
	ui.Click("文件夹：tree")
	job = welcomeJob(t, ctrl, "device.directories")
	job.Apply(m, json.RawMessage(`{"path":"/Users/remote/tree","parent":"/Users/remote","home":"/Users/remote","entries":[{"name":"child","path":"/Users/remote/tree/child"}]}`), nil)
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	middle, _ := ui.Find("文件夹：child")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("文件夹：child")
	if middle.X <= end.X+1 {
		t.Fatal("subsequent directory navigation lost its slide", middle, end)
	}
}

func TestCreateRemoteWorkspaceAddsExtraFoldersWithPlus(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	v.beginWorkspace("air")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("远程附加目录"); ok {
		t.Fatal("new workspace still exposes the old multiline path field")
	}
	if err := ui.Click("添加文件夹"); err != nil {
		t.Fatal("additional directories have no plus action", err)
	}
	if v.directoryPicker == nil || v.directoryPicker.title != "选择附加目录" {
		t.Fatal("plus did not open the owning device's folder picker")
	}
}

func TestDirectoryPaginationDoesNotRepeatFolderIdentity(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = true
	m.DeviceStates["air"].WorkerMinor = 23
	ctrl := app.NewController("")
	v := New(m, ctrl, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("选择主目录")
	job := welcomeJob(t, ctrl, "device.directories")
	job.Apply(m, json.RawMessage(`{"path":"/Users/remote","home":"/Users/remote","entries":[{"name":"one","path":"/Users/remote/one"}],"next":1,"more":true}`), nil)
	p := v.directoryPicker
	v.readDirectory(p, p.page.Path, p.page.Next, 0)
	job = welcomeJob(t, ctrl, "device.directories")
	job.Apply(m, json.RawMessage(`{"path":"/Users/remote","home":"/Users/remote","entries":[{"name":"one","path":"/Users/remote/one"},{"name":"two","path":"/Users/remote/two"}],"next":3}`), nil)
	if len(p.page.Entries) != 2 {
		t.Fatal("overlapping directory pages repeated a native row identity", p.page.Entries)
	}
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("one") || !ui.HasText("two") {
		t.Fatal("pagination lost a folder", ui.Texts())
	}
}

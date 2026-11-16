package ui

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/domain"
)

func TestFileActivityPreservesFullAddedAndDeletedContent(t *testing.T) {
	for _, kind := range []string{"add", "delete"} {
		for _, source := range []string{
			"package ui\n\nfunc testLayout() {}\n",
			"+literal content\n-literal content\n@@ -1 +1 @@\n",
		} {
			t.Run(fmt.Sprintf("%s/%q", kind, source[:8]), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				file := domain.FileChange{Path: "/device/source.go", Kind: kind, Patch: source}
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(20).Children(func() { v.activityDiff(c, c.Theme(), "patch", file) })
				}, 620, 350)
				ui.Advance(time.Second)
				state := v.activityPatches["patch"]
				if state == nil || state.State.Old == nil || state.State.New == nil {
					t.Fatal("missing native file-change buffers")
				}
				old, next := state.State.Old.String(), state.State.New.String()
				if kind == "add" && (old != "" || next != source) || kind == "delete" && (old != source || next != "") {
					t.Fatal("file contents were mistaken for diff markers", old, next)
				}
			})
		}
	}
}

func TestActivityDiffHeightMatchesNativeRowsAndWrapping(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{340, 660} {
			for _, patch := range []string{
				"@@ -1 +1 @@\n-old\n+new",
				"@@ -36,3 +36,3 @@\n \t\t\t\t\t\tn.Column(c).Fill().Background(theme.Background).Children(func() { v.sessionWorkBody(c, theme, kind, works, width) })\n-\t\t\t\t\t}, width, 320)\n+\t\t\t\t\t}, int(width), 320)\n \t\t\t\t\tui.SetDark(dark)",
			} {
				t.Run(fmt.Sprintf("dark=%v/width=%d/bytes=%d", dark, width, len(patch)), func(t *testing.T) {
					v := New(layoutModel(), nil, "")
					file := domain.FileChange{Path: "/device/source.go", Kind: "update", Patch: patch}
					ui := n.NewTester(func(c *n.Context) {
						theme := applyAppearance(c, v.Model.Preferences)
						n.Column(c).Fill().Padding(20).Background(theme.Background).Children(func() { v.activityDiff(c, theme, "patch", file) })
					}, width, 600)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					box, _ := ui.Find("文件补丁：" + file.Path)
					// A fully visible native viewport measures all rows, using the
					// same scrollbar gutter and wraps as the bounded preview.
					d := editor.DiffState{Layout: editor.DiffUnified, Wrap: true}
					files, err := editor.ParsePatch(patch)
					if err != nil {
						t.Fatal(err)
					}
					d.SetPatch(files[0])
					state := v.activityPatches["patch"]
					d.Font, d.LineHeight, d.Indicators, d.Theme = state.State.Font, state.State.LineHeight, state.State.Indicators, state.State.Theme
					fitUI := n.NewTester(func(c *n.Context) {
						theme := applyAppearance(c, v.Model.Preferences)
						n.Column(c).Fill().Padding(20).Background(theme.Background).Children(func() { editor.DiffView(c, &d).FillWidth().Height(560) })
					}, width, 600)
					fitUI.SetDark(dark)
					fitUI.Advance(time.Second)
					natural := d.ContentHeight()
					want := min(float32(220), natural)
					if math.Abs(float64(box.H-want)) > .5 {
						t.Errorf("diff reserves unused bottom height or clips soft wraps: got=%g native=%g cap=%g", box.H, natural, want)
					}
					if box.W != float32(width-40) {
						t.Error("height sizing changes available code width", box)
					}
				})
			}
		}
	}
}

func TestFileActivityRetainsUnifiedPatchAndUnknownText(t *testing.T) {
	v := New(layoutModel(), nil, "")
	file := domain.FileChange{Path: "/device/source.go", Kind: "update", Patch: "@@ -1 +1 @@\n-old\n+new"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(20).Children(func() { v.activityDiff(c, c.Theme(), "patch", file) })
	}, 620, 350)
	ui.Advance(time.Second)
	state := v.activityPatches["patch"]
	if state == nil || state.State.Old.String() != "old" || state.State.New.String() != "new" {
		t.Fatal("valid update patch loses its before/after contents")
	}
	file.Patch = "unrecognized patch format\nretain every line"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText(file.Patch) {
		t.Fatal("unrecognized patch was replaced with an empty diff")
	}
}

func TestActivityDiffHeightFollowsViewportResize(t *testing.T) {
	v := New(layoutModel(), nil, "")
	file := domain.FileChange{Path: "/device/source.go", Kind: "add", Patch: strings.Repeat("source ", 12)}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(20).Children(func() { v.activityDiff(c, c.Theme(), "patch", file) })
	}, 300, 400)
	ui.Advance(time.Second)
	narrow, _ := ui.Find("文件补丁：" + file.Path)
	ui.SetSize(1000, 400)
	ui.Advance(time.Second)
	wide, _ := ui.Find("文件补丁：" + file.Path)
	if wide.H != 20 || narrow.H <= wide.H {
		t.Fatal("resizing leaves blank wrapped rows or clips source", narrow, wide)
	}
	ui.SetSize(300, 400)
	ui.Advance(time.Second)
	again, _ := ui.Find("文件补丁：" + file.Path)
	if again.H != narrow.H {
		t.Fatal("narrowing does not restore visual row height", narrow, again)
	}
}

func TestFileActivityCacheIncludesChangeKind(t *testing.T) {
	v := New(layoutModel(), nil, "")
	file := domain.FileChange{Path: "/device/source.txt", Kind: "add", Patch: "+literal text"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(20).Children(func() { v.activityDiff(c, c.Theme(), "patch", file) })
	}, 620, 350)
	ui.Advance(time.Second)
	file.Kind = "delete"
	ui.Frame()
	ui.Advance(time.Second)
	state := v.activityPatches["patch"]
	if state == nil || state.State.Old.String() != file.Patch || state.State.New.String() != "" {
		t.Fatal("same contents reuse the wrong side after change kind updates")
	}
}

func TestFileActivityNativeContentAndCompactHeight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, lines := range []int{1, 97} {
			t.Run(fmt.Sprintf("dark=%v/lines=%d", dark, lines), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				content := "package ui // native activity preview"
				if lines > 1 {
					content += "\n" + strings.Repeat("// preserve the actual source contents\n", lines-1)
				}
				item := domain.Item{ID: "file", Kind: "tool", Presentation: "files", Name: "编辑 navigation_alignment_test.go", Status: "completed", Final: true, Files: []domain.FileChange{{Path: "/device/navigation_alignment_test.go", Kind: "add", Patch: content}}}
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() { v.item(c, c.Theme(), item) })
				}, 820, 450)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if err := ui.Click(item.Name); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("file-dark-%v-lines-%d.png", dark, lines))
				box, ok := ui.Find("文件补丁：/device/navigation_alignment_test.go")
				if !ok || box.H > 220 || lines == 1 && box.H > 48 {
					t.Error("diff reserves height unrelated to its rendered content", box, ok)
				}
				im := ui.Image()
				ink := 0
				for y := int(box.Y); y < int(box.Y+box.H); y++ {
					for x := int(box.X + 90); x < int(box.X+box.W-20); x++ {
						p, bg := im.RGBAAt(x, y), im.RGBAAt(int(box.X+box.W-10), y)
						if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 50 {
							ink++
						}
					}
				}
				if ink < 50 {
					t.Error("native diff paints a large blank surface without source text", ink, box)
				}
				if lines > 1 {
					before := contextPixels(ui, box)
					ui.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, 2000)
					ui.Frame()
					ui.Advance(250 * time.Millisecond)
					if bytes.Equal(before, contextPixels(ui, box)) {
						t.Error("long source cannot scroll inside its bounded preview")
					}
				}
			})
		}
	}
}

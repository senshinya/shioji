package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func sidebarInkBounds(t *testing.T, frame *image.RGBA, box n.Rect, scale float32, background color.RGBA) image.Rectangle {
	t.Helper()
	left, top, right, bottom := frame.Bounds().Max.X, frame.Bounds().Max.Y, -1, -1
	for y := int(box.Y * scale); y < int((box.Y+box.H)*scale); y++ {
		for x := int(box.X * scale); x < int((box.X+box.W)*scale); x++ {
			pixel := frame.RGBAAt(x, y)
			if max(absChannel(pixel.R, background.R), absChannel(pixel.G, background.G), absChannel(pixel.B, background.B)) > 30 {
				left, top, right, bottom = min(left, x), min(top, y), max(right, x), max(bottom, y)
			}
		}
	}
	if right < left {
		t.Fatalf("missing sidebar ink in %+v", box)
	}
	return image.Rect(left, top, right+1, bottom+1)
}

func TestSidebarNavigationColumnsShareVisibleAxes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			m := navigationFixture()
			m.CompletedReplies = map[string]app.CompletedReply{"other": {Unread: true}}
			width := 1280
			if dark {
				width = 940
			}
			ui := n.NewTester(New(m, nil, "").View, width, 800)
			ui.SetDark(dark)
			const scale = 2
			ui.SetScale(scale)
			ui.Advance(time.Second)
			find := func(label string) n.Rect {
				t.Helper()
				box, ok := ui.Find(label)
				if !ok {
					t.Fatalf("missing sidebar element %q", label)
				}
				return box
			}
			frame := ui.Image()
			background := frame.RGBAAt(12*scale, 400*scale)
			ink := func(box n.Rect) (float64, float64) {
				bounds := sidebarInkBounds(t, frame, box, scale, background)
				return float64(bounds.Min.X) / scale, float64(bounds.Max.X) / scale
			}
			center := func(box n.Rect) float64 {
				left, right := ink(box)
				return (left + right) / 2
			}
			near := func(label string, got, want float64) {
				t.Helper()
				if math.Abs(got-want) > 1 {
					t.Errorf("%s at %g, want %g", label, got, want)
				}
			}

			workspace := find("Example")
			disclosure := workspace
			disclosure.W = 20
			workspaceTitle := workspace
			workspaceTitle.X += 20
			workspaceTitle.W = 85
			workspaceLeft, _ := ink(workspaceTitle)
			for _, label := range []string{"新建会话", "待我处理", "定时任务"} {
				leading := find(label)
				title := leading
				leading.W = 23
				title.X += 23
				title.W = 85
				near(label+" icon", center(leading), center(disclosure))
				left, _ := ink(title)
				near(label+" title", left, workspaceLeft)
			}
			near("device dock icon", center(find("设备图标")), center(disclosure))
			for _, label := range []string{"搜索", "设置"} {
				near(label, center(find(label)), center(find("创建工作区")))
			}
			_, countRight := ink(find("待处理会话数：1"))
			_, deviceRight := ink(find(m.LocalName()))
			near("attention count", countRight, deviceRight)
			entry, session := find("新建会话"), find("Layout")
			near("top entry trailing edge", float64(entry.X+entry.W), float64(session.X+session.W))
		})
	}
}

func TestSidebarActionsShareForeground(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			m := navigationFixture()
			m.SessionID = ""
			m.CompletedReplies = map[string]app.CompletedReply{"other": {Unread: true}}
			const scale = 2
			ui := n.NewTester(New(m, nil, "").View, 1280, 800)
			ui.SetDark(dark)
			ui.SetScale(scale)
			for _, page := range []string{"新建会话", "定时任务"} {
				if err := ui.Click(page); err != nil {
					t.Fatal(err)
				}
				ui.Move(0, 0)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("sidebar-dark-%t-%s.png", dark, page))
				frame := ui.Image()
				foreground := func(label string) uint8 {
					t.Helper()
					row, ok := ui.Find(label)
					if !ok {
						t.Fatalf("missing sidebar action %q", label)
					}
					// Sample the text columns, excluding the icon and attention count.
					ink := frame.RGBAAt(int((row.X+26)*scale), int(row.Y*scale)).R
					for y := int(row.Y * scale); y < int((row.Y+row.H)*scale); y++ {
						for x := int((row.X + 26) * scale); x < int((row.X+80)*scale); x++ {
							red := frame.RGBAAt(x, y).R
							if dark {
								ink = max(ink, red)
							} else {
								ink = min(ink, red)
							}
						}
					}
					return ink
				}
				want := foreground("新建会话")
				for _, action := range []string{"待我处理", "定时任务"} {
					if got := foreground(action); absChannel(got, want) > 3 {
						t.Errorf("%s foreground on %s = %d, want %d", action, page, got, want)
					}
				}
			}
		})
	}
}

func TestSidebarSectionHeadingsShareVisibleLeftEdge(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, width := range []int{940, 1280} {
				for _, device := range []string{"MacMini at Home", "这台 Mac"} {
					t.Run(fmt.Sprintf("dark=%v/scale=%g/width=%d/device=%s", dark, scale, width, device), func(t *testing.T) {
						m := navigationFixture()
						m.LocalDeviceName = device
						m.Snapshot.Workspaces[0].Name = "shioji"
						if err := m.SetSessionPinned(app.SessionLocation{DeviceID: "B", WorkspaceID: "remote", SessionID: "remote-session"}, true); err != nil {
							t.Fatal(err)
						}
						v := New(m, nil, "")
						ui := n.NewTester(v.View, width, 600)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.Advance(time.Second)
						caption, _ := ui.Find("工作区")
						pinned, _ := ui.Find("置顶")
						workspace, _ := ui.Find("shioji")
						im := ui.Image()
						bg := im.RGBAAt(int((caption.X+caption.W/2)*scale), int((caption.Y+caption.H/2)*scale))
						labelInk := sidebarInkBounds(t, im, caption, scale, bg)
						pinnedInk := sidebarInkBounds(t, im, pinned, scale, bg)
						arrowInk := sidebarInkBounds(t, im, n.Rect{X: workspace.X, Y: workspace.Y, W: 20, H: workspace.H}, scale, bg)
						saveNativeSkillImage(t, ui, fmt.Sprintf("workspace-edges-dark-%v-scale-%g-width-%d-%s.png", dark, scale, width, device))
						if delta := float64(labelInk.Min.X-arrowInk.Min.X) / float64(scale); math.Abs(delta) > 1 {
							t.Errorf("heading and disclosure have different visible left edges: %g DIP", delta)
						}
						if delta := float64(labelInk.Min.X-pinnedInk.Min.X) / float64(scale); math.Abs(delta) > 1 {
							t.Errorf("section headings have different visible left edges: %g DIP", delta)
						}
					})
				}
			}
		}
	}
}

func TestPinnedSessionMarkerCentersOnWorkspaceDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		dark  bool
		title string
	}{
		{"light", 1280, false, "Remote running"},
		{"dark-narrow", 940, true, "远程 Agent 会话"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := navigationFixture()
			m.Snapshot.Sessions[2].Title = tc.title
			v := New(m, nil, "")
			ui := n.NewTester(v.View, tc.width, 800)
			ui.SetDark(tc.dark)
			ui.Advance(time.Second)
			if err := ui.RightClick(tc.title); err != nil {
				t.Fatal(err)
			}
			if err := ui.ChooseMenuItem("置顶会话"); err != nil {
				t.Fatal(err)
			}
			if err := ui.Click("Remote workspace"); err != nil {
				t.Fatal(err)
			}
			ui.Move(0, 0)
			ui.Advance(time.Second)
			for _, scale := range []float32{1, 2} {
				ui.SetScale(scale)
				for _, state := range []domain.ExecutionState{"running", "completed", "idle", "failed", "unknown", "read-only"} {
					s := &m.Snapshot.Sessions[2]
					s.Status, s.Origin = state, nil
					if state == "read-only" {
						s.Status = "completed"
						s.Origin = &domain.ConversationOrigin{SourceRef: "imported"}
					}
					m.CompletedReplies = map[string]app.CompletedReply{s.ID: {Unread: state == "completed"}}
					ui.Frame()
					if state == "running" || state == "completed" {
						saveNativeSkillImage(t, ui, fmt.Sprintf("pinned-%s-%s-%gx.png", tc.name, state, scale))
					}
					row, hasRow := ui.Find(tc.title)
					if !hasRow {
						t.Fatal("missing pinned row")
					}
					im := ui.Image()
					markerCenter := func(r n.Rect) float64 {
						bg := im.RGBAAt(int((row.X+2)*scale), int(r.Y*scale))
						bounds := sidebarInkBounds(t, im, r, scale, bg)
						return float64(bounds.Min.X+bounds.Max.X) / (2 * float64(scale))
					}
					row.W = 20
					iconX := markerCenter(row)
					for _, name := range []string{"Example", "Remote workspace"} {
						workspace, ok := ui.Find(name)
						if !ok || workspace.Y <= row.Y {
							t.Fatalf("missing workspace %s below the pinned row", name)
						}
						workspace.W = 20
						arrowX := markerCenter(workspace)
						if math.Abs(iconX-arrowX) > 1 {
							t.Errorf("%s at %gx: status center x = %g, want %s disclosure center x = %g", state, scale, iconX, name, arrowX)
						}
					}
				}
			}
		})
	}
}

// An empty trailing action slot must not supply the row's text baseline.
// Check actual native ink, including hover/pin changes and Retina rendering.
func TestSessionNavigationInkAlignment(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, title := range []string{"HHH", "国国国", "国 H 国"} {
				for _, status := range []string{"running", "completed"} {
					t.Run(fmt.Sprintf("dark=%v/scale=%g/%s/%s", dark, scale, title, status), func(t *testing.T) {
						m := navigationFixture()
						v := New(m, nil, "")
						s := m.Snapshot.Sessions[0]
						s.Title, s.Status = title, domain.ExecutionState(status)
						r := navigationRow{session: s, workspace: m.Snapshot.Workspaces[0], presence: 1}
						ui := n.NewTester(func(c *n.Context) {
							n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() {
								v.navigationSessionRow(c, c.Theme(), r)
							})
						}, 300, 90)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.SetPreferences(n.Preferences{ReduceMotion: true})
						ui.Advance(time.Second)
						row, ok := ui.Find(title)
						if !ok {
							t.Fatal("missing session row")
						}
						inkCenter := func(box n.Rect) float64 {
							t.Helper()
							im := ui.Image()
							bg := im.RGBAAt(int((row.X+2)*scale), int((row.Y+row.H/2)*scale))
							lo, hi := im.Bounds().Max.Y, -1
							for y := int(math.Floor(float64(box.Y * scale))); y < int(math.Ceil(float64((box.Y+box.H)*scale))); y++ {
								for x := int(box.X * scale); x < int((box.X+box.W)*scale); x++ {
									p := im.RGBAAt(x, y)
									if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
										lo, hi = min(lo, y), max(hi, y)
									}
								}
							}
							if hi < 0 {
								t.Fatal("missing native ink", box)
							}
							return float64(lo+hi+1) / (2 * float64(scale))
						}
						var previous float64
						for _, pinned := range []bool{false, true} {
							r.pinned = pinned
							textStart, markerStart, markerWidth := float32(36), float32(20), float32(10)
							if pinned {
								textStart, markerStart, markerWidth = 23, 4, 16
							}
							for _, hover := range []bool{false, true, false} {
								if hover {
									ui.Move(row.X+row.W-20, row.Y+row.H/2)
								} else {
									ui.Move(0, 0)
								}
								ui.Frame()
								ui.Advance(time.Second)
								name := fmt.Sprintf("dark-%v-scale-%g-%s-%s-pin-%v-hover-%v.png", dark, scale, title, status, pinned, hover)
								saveNativeSkillImage(t, ui, name)
								text := inkCenter(n.Rect{X: row.X + textStart, Y: row.Y, W: 65, H: row.H})
								marker := inkCenter(n.Rect{X: row.X + markerStart, Y: row.Y, W: markerWidth, H: row.H})
								if delta := text - float64(row.Y+row.H/2); math.Abs(delta) > 2 {
									t.Errorf("title sinks within the session row: delta=%g", delta)
								}
								if delta := marker - text; math.Abs(delta) > 1 {
									t.Errorf("status is offset from title: delta=%g", delta)
								}
								if hover || pinned {
									action := inkCenter(n.Rect{X: row.X + row.W - 26, Y: row.Y + 3, W: 16, H: row.H - 6})
									if delta := action - text; math.Abs(delta) > 1 {
										t.Errorf("trailing action is offset from title: delta=%g", delta)
									}
								}
								if previous != 0 && math.Abs(text-previous) > .5 {
									t.Errorf("hover or pin moves title: before=%g after=%g", previous, text)
								}
								previous = text
							}
						}
					})
				}
			}
		}
	}
}

package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestFileLinkActionsShareTheContentReadingEdge(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, loading := range []bool{false, true} {
			for _, scale := range []float32{1, 2} {
				t.Run(fmt.Sprintf("dark-%v/loading-%v/scale-%g", dark, loading, scale), func(t *testing.T) {
					v := New(layoutModel(), nil, "")
					v.fileLink = &fileLinkState{session: "s", workspace: v.Model.Details["s"].Session.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: "douyin_admin_platform.thrift", loading: loading, error: "文件不在本会话的已接纳目录或设备临时目录中"}
					ui := n.NewTester(func(c *n.Context) { v.fileLinkDialog(c, c.Theme()) }, 420, 260)
					ui.SetDark(dark)
					ui.SetScale(scale)
					ui.Advance(time.Second)
					name, message := "重试", v.fileLink.error
					if loading {
						name, message = "取消文件下载", "正在连接所属设备…"
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("file-link-dark-%v-loading-%v-scale-%g.png", dark, loading, scale))
					action, aok := ui.Find(name)
					body, bok := ui.Find(message)
					if !aok || !bok {
						t.Fatal("missing file link content", ui.Texts())
					}
					// Compare actual text pixels, including the button's internal inset.
					inkLeft := func(r n.Rect) float64 {
						img := ui.Image()
						background := img.RGBAAt(int((r.X+r.W/2)*scale), int(r.Y*scale))
						for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
							for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
								p := img.RGBAAt(x, y)
								if math.Abs(float64(p.R)-float64(background.R)) > 40 {
									return float64(x) / float64(scale)
								}
							}
						}
						t.Fatal("missing text ink", r)
						return 0
					}
					if delta := inkLeft(action) - inkLeft(body); math.Abs(delta) > 1 {
						t.Fatalf("action text is offset from content by %.1f DIP", delta)
					}
					if err := ui.Click(name); err != nil {
						t.Fatal(err)
					}
					if loading && v.fileLink != nil {
						t.Fatal("aligned cancel action no longer closes download")
					}
				})
			}
		}
	}
}

package ui

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestRequestSourceKeepsStatusOnOneLine(t *testing.T) {
	const command = "ls -1 internal/ui/*agent* internal/ui/*device* internal/ui/*config* internal/ui/*mcp* 2>/dev/null; " +
		"echo \"---\"; ls -1 cmd/shioji-mobile/*agent* cmd/shioji-mobile/*device* cmd/shioji-mobile/*mcp* 2>/dev/null"
	const suffix = "· 已结束"
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{
				{ID: "approval", Kind: "request", Presentation: "approval", Text: command, Status: "resolved", Final: true},
				{ID: "short-approval", Kind: "request", Presentation: "approval", Text: "git log -n 15 --oneline", Status: "invalidated", Final: true},
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 650)
			ui.SetDark(dark)
			ui.SetScale(2)
			for _, width := range []int{1240, 900} {
				ui.SetSize(width, 650)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("dark-%v-width-%d.png", dark, width))

				row, found := ui.Find(command + " " + suffix)
				status, shown := ui.Find(suffix)
				short, shortShown := ui.Find("· 已失效")
				if !found || !shown || !shortShown || row.H < 40 {
					t.Fatalf("expected a wrapped command and two statuses at width %d: row=%+v status=%+v short=%+v", width, row, status, short)
				}
				if status.H != short.H || math.Abs(float64(status.W-short.W)) > 1 ||
					math.Abs(float64(status.Y-row.Y)) > 1 || status.X+status.W > row.X+row.W+.5 {
					t.Errorf("status must fit on the first line at width %d: row=%+v status=%+v short=%+v", width, row, status, short)
				}
			}
		})
	}
}

func TestLongApprovalCommandScrollsAboveActions(t *testing.T) {
	const title = "待审批"
	command := "for board in conversation changes settings; do\n" +
		strings.Repeat("  xcrun simctl ui \"$UDID\" appearance light\n"+
			"  xcrun simctl launch \"$UDID\" ai.shioji.mobile \"--preview-board-$board\" >/dev/null\n"+
			"  xcrun simctl io \"$UDID\" screenshot \"$OUT/ios-$board-light.png\" >/dev/null\n", 12) +
		"done"
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Requests = []domain.PendingRequest{{
				ID: "approval", SessionID: "s", Kind: "approval", State: "pending", Title: command,
				Choices: []domain.RequestChoice{{ID: "accept", Label: "Allow"}, {ID: "decline", Label: "Deny"}},
			}}
			m.Details["s"] = d
			const width, height, scale = 900, 600, 2
			ui := n.NewTester(New(m, nil, "").View, width, height)
			ui.SetDark(dark)
			ui.SetScale(scale)
			ui.Advance(time.Second)

			check := func(stage string) (n.Rect, n.Rect) {
				t.Helper()
				saveNativeSkillImage(t, ui, fmt.Sprintf("pending-dark-%v-%s.png", dark, stage))
				panel, panelShown := ui.Find("待处理请求卡片")
				heading, headingShown := ui.Find(title)
				body, bodyShown := ui.Find("问题选项内容")
				allow, allowShown := ui.Find("Allow")
				deny, denyShown := ui.Find("Deny")
				if !panelShown || !headingShown || !bodyShown || !allowShown || !denyShown {
					t.Fatalf("missing approval content: %v", ui.Texts())
				}
				if body.H < 40 || body.H > .28*height+.5 || heading.Y+heading.H > body.Y ||
					body.Y+body.H >= min(allow.Y, deny.Y) || max(allow.Y+allow.H, deny.Y+deny.H) > panel.Y+panel.H {
					t.Errorf("approval preview and controls must have separate bounds: panel=%+v heading=%+v body=%+v allow=%+v deny=%+v", panel, heading, body, allow, deny)
				}
				img := ui.Image()
				// The command must leave the gap above the button row clear.
				previewBottom := body.Y + body.H
				actionTop := allow.Y - (processingPanelActionHeight-allow.H)/2
				for y := int((previewBottom + 2) * scale); y < int((actionTop-2)*scale); y++ {
					bg := img.RGBAAt(int((body.X-2)*scale), y)
					for x := int(body.X * scale); x < int((body.X+body.W-8)*scale); x++ {
						if absChannel(img.RGBAAt(x, y).R, bg.R) > 35 {
							t.Errorf("command paints outside its scroll viewport at pixel %d,%d", x, y)
							return body, allow
						}
					}
				}
				return body, allow
			}
			body, allow := check("top")
			firstLine := n.Rect{X: body.X * scale, Y: body.Y * scale, W: 320 * scale, H: 20 * scale}
			before := contextPixels(ui, firstLine)
			ui.Scroll(body.X+body.W/2, body.Y+body.H/2, 0, 2000)
			ui.Advance(time.Second)
			afterBody, afterAllow := check("scrolled")
			if bytes.Equal(before, contextPixels(ui, firstLine)) || afterBody != body || afterAllow != allow {
				t.Error("scrolling did not move the visible command with the actions pinned")
			}
		})
	}
}

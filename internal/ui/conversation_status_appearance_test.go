package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestReadOnlyNoticeSharesRequestDisclosureGeometry(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1120, 850} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				m := layoutModel()
				m.WorkerMinor = 41
				s := m.Snapshot.Sessions[0]
				s.Origin = &domain.ConversationOrigin{SourceRef: "external", Truncated: true}
				m.Snapshot.Sessions[0] = s
				d := m.Details[s.ID]
				d.Session = s
				m.Details[s.ID] = d
				v := New(m, nil, "")
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				name := fmt.Sprintf("status-%v-%d", dark, width)
				saveNativeSkillImage(t, ui, name+"-expanded.png")
				panel, _ := ui.Find("会话状态面板")
				title, _ := ui.Find("只读会话")
				toggle, _ := ui.Find("折叠会话状态")
				if toggle.X > title.X || toggle.W < title.W {
					t.Error("disclosure does not include the title", toggle, title)
				}
				if err := ui.Click("折叠会话状态"); err != nil {
					t.Fatal(err)
				}
				ui.Move(0, 0)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, name+"-folded.png")
				folded, _ := ui.Find("会话状态面板")
				// Compare with the existing request notification, rather than a
				// second hardcoded definition of compact notification density.
				other := layoutModel()
				rd := other.Details["s"]
				rd.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "检查哪部分？", Custom: true}}}}
				other.Details["s"] = rd
				other.Collapsed["s"] = true
				reference := n.NewTester(New(other, nil, "").View, width, 820)
				reference.SetDark(dark)
				reference.Advance(time.Second)
				request, _ := reference.Find("待处理请求卡片")
				if math.Abs(float64(folded.H-request.H)) > 1 {
					t.Error("folded status has a different density from request notifications", folded, request, "expanded", panel)
				}
				if err := ui.Click("展开会话状态"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				if !ui.HasText("设备详情") || m.Drafts[m.DraftKey()] != "" {
					t.Error("disclosure lost its action or changed the draft")
				}
				if dir := os.Getenv("SHIOJI_NOTICE_RECORDING"); dir != "" && width == 1120 {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
					// Native production View, isolated model fixtures, 30 fps.
					// Fold/reopen, reverse quickly, update while folded, then
					// add error details in the expanded panel.
					for frame := 0; frame < 210; frame++ {
						label := ""
						switch frame {
						case 15, 78, 90:
							label = "折叠会话状态"
						case 45, 81, 120:
							label = "展开会话状态"
						case 105:
							m.WorkerMinor = 42
							v.importCompletion = &conversationImportCompletion{session: s.ID, error: "暂时无法读取来源历史，请稍后重试。"}
						case 150:
							label = "查看详情"
						case 180:
							v.importCompletion.error += "\n会话和草稿仍保留在当前设备。"
						}
						if label != "" {
							if err := ui.Click(label); err != nil {
								t.Fatal("recording action", frame, label, err)
							}
						}
						ui.Advance(time.Second / 30)
						ui.Move(0, 0)
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					}
				}
			})
		}
	}
}

package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

// An option collection is content of the request, not another raised card.
func TestProcessingOptionsUseOneSurface(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, form := range []bool{false, true} {
			m := layoutModel()
			d := m.Details["s"]
			r := domain.PendingRequest{ID: "r", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "先检查哪一部分？", Options: []domain.Option{{Label: "连接恢复"}, {Label: "草稿保留"}}}}}
			if form {
				r.Kind = "form"
				r.Questions = nil
				r.Fields = []domain.FormField{{ID: "checks", Title: "检查项目", Type: "array", Options: []domain.FormOption{{Value: "a", Label: "连接恢复"}, {Value: "b", Label: "草稿保留"}}}}
			}
			d.Requests = []domain.PendingRequest{r}
			m.Details["s"] = d
			ui := n.NewTester(New(m, nil, "").View, 1100, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			panel, ok := ui.Find("待处理请求卡片")
			if !ok {
				t.Fatal("missing pending request")
			}
			option, ok := ui.Find("连接恢复")
			if !ok {
				t.Fatal("missing native option")
			}
			im := ui.Image()
			y := int(option.Y + option.H/2)
			outer := im.RGBAAt(int(panel.X+3), y)
			inner := im.RGBAAt(int(option.X+option.W-3), y)
			if outer != inner {
				t.Fatalf("option group adds a second surface (dark=%v form=%v): panel=%v option=%v", dark, form, outer, inner)
			}
		}
	}
}

func TestProcessingRecoveryTasksUseOneSurface(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		m.WorkerMinor = 8
		d := m.Details["s"]
		d.Session.Status = "unknown"
		d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanStopTasks: true, Tasks: []domain.RecoveryTask{{ID: "p", PID: 2418, State: "running", Command: "go test", ProcessCommand: "go test ./internal/transport"}}}
		m.Details["s"] = d
		m.Snapshot.Sessions[0] = d.Session
		ui := n.NewTester(New(m, nil, "").View, 1100, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		if err := ui.Click("查看处理方法"); err != nil {
			t.Fatal(err)
		}
		ui.Move(0, 0)
		ui.Advance(time.Second)
		panel, _ := ui.Find("执行恢复")
		row, ok := ui.Find("go test")
		if !ok {
			t.Fatal("no source task detail")
		}
		im := ui.Image()
		y := int(row.Y + row.H/2)
		outer := im.RGBAAt(int(panel.X+3), y)
		inner := im.RGBAAt(int(row.X+row.W-3), y)
		if outer != inner {
			t.Fatalf("process list adds a second surface (dark=%v): panel=%v row=%v", dark, outer, inner)
		}
	}
}

func TestProcessingDisclosureAlignsTitleWithBody(t *testing.T) {
	const title = "需要处理"
	const body = "需要处理当前请求。"
	for _, kind := range []string{"approval", "readonly"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%t", kind, dark), func(t *testing.T) {
				m := layoutModel()
				detail := m.Details["s"]
				titleLabel := title
				if kind == "approval" {
					detail.Requests = []domain.PendingRequest{{ID: "approval", SessionID: "s", Kind: "approval", State: "pending", Title: title, Text: body}}
				} else {
					detail.Session.Origin = &domain.ConversationOrigin{SourceRef: "external", Reason: body}
					m.Snapshot.Sessions[0] = detail.Session
					titleLabel = "只读会话"
				}
				m.Details["s"] = detail
				ui := n.NewTester(New(m, nil, "").View, 850, 600)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)
				heading, headingFound := ui.Find(titleLabel)
				content, bodyFound := ui.Find(body)
				if !headingFound || !bodyFound {
					t.Fatalf("missing card content: %v", ui.Texts())
				}
				img := ui.Image()
				// Start after the symbol and look for the first painted
				// column in each line, including any layout or glyph inset.
				firstText := func(row n.Rect) float64 {
					bg := int(img.RGBAAt(int(content.X*2), int(row.Y*2)+1).R)
					for x := int((content.X - 4) * 2); x < int((content.X+14)*2); x++ {
						for y := int(row.Y * 2); y < int((row.Y+row.H)*2); y++ {
							pixel := int(img.RGBAAt(x, y).R)
							if delta := pixel - bg; delta < -55 || delta > 55 {
								return float64(x) / 2
							}
						}
					}
					t.Fatalf("missing visible text in %v", row)
					return 0
				}
				if delta := firstText(heading) - firstText(content); math.Abs(delta) > 1 {
					t.Fatalf("heading text is %.1f DIP from the body column", delta)
				}
			})
		}
	}
}

func TestRemoteAttachmentCardSharesProcessingPanelInsetAndCorner(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	detail := v.Model.Details["s"]
	detail.Requests = []domain.PendingRequest{{ID: "approval", SessionID: "s", Kind: "approval", State: "pending", Title: "允许执行此命令？", Text: "go test ./internal/ui"}}
	v.Model.Details["s"] = detail
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetScale(2)
	ui.Advance(time.Second)
	request, requestFound := ui.Find("待处理请求卡片")
	upload, uploadFound := ui.Find("远程附件上传")
	title, titleFound := ui.Find("上传文件到 MacBook Air？")
	if !requestFound || !uploadFound || !titleFound {
		t.Fatalf("missing stacked processing cards: %v", ui.Texts())
	}
	if upload.X != request.X || upload.W != request.W || math.Abs(float64(title.X-upload.X-17)) > .5 {
		t.Errorf("upload title and surface must follow the processing inset: request=%v upload=%v title=%v", request, upload, title)
	}
	img := ui.Image()
	firstFill := func(box n.Rect) int {
		x0, y := int(box.X*2), int(box.Y*2)+5
		fill := img.RGBAAt(x0+60, y)
		for x := x0; x < x0+32; x++ {
			if img.RGBAAt(x, y) == fill {
				return x - x0
			}
		}
		t.Fatal("missing solid panel fill near top-left corner")
		return 0
	}
	if a, b := firstFill(upload), firstFill(request); a != b {
		t.Errorf("upload corner fill starts at %.1f DIP, request at %.1f DIP", float64(a)/2, float64(b)/2)
	}
}

func TestExpandedProcessingPanelsBalanceVerticalClearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, kind := range []string{"approval", "status", "recovery", "upload"} {
			t.Run(fmt.Sprintf("%s/dark=%t", kind, dark), func(t *testing.T) {
				m := layoutModel()
				detail := m.Details["s"]
				var v *View
				var panelLabel, titleLabel, actionLabel string
				switch kind {
				case "approval":
					panelLabel, titleLabel, actionLabel = "待处理请求卡片", "允许执行此命令？", "允许这一次"
					detail.Requests = []domain.PendingRequest{{ID: "approval", SessionID: "s", Kind: "approval", State: "pending", Title: titleLabel, Text: "go test ./internal/ui"}}
					m.Details["s"] = detail
				case "status":
					panelLabel, titleLabel, actionLabel = "会话状态面板", "只读会话", "设备详情"
					m.WorkerMinor = 41
					detail.Session.Origin = &domain.ConversationOrigin{SourceRef: "external", Truncated: true}
					m.Snapshot.Sessions[0], m.Details["s"] = detail.Session, detail
				case "recovery":
					panelLabel, titleLabel, actionLabel = "执行恢复", "暂时无法继续", "重试恢复"
					m.WorkerMinor = 7
					detail.Session.Status = "unknown"
					detail.Session.Recovery = &domain.SessionRecovery{Phase: "attention"}
					m.Snapshot.Sessions[0], m.Details["s"] = detail.Session, detail
				case "upload":
					panelLabel, titleLabel, actionLabel = "远程附件上传", "上传文件到 MacBook Air？", "上传并引用"
					var key string
					v, key = remoteAttachmentFixture(t)
					stageRemoteAttachment(t, v, key)
				}
				if v == nil {
					v = New(m, nil, "")
				}
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)
				panel, hasPanel := ui.Find(panelLabel)
				title, hasTitle := ui.Find(titleLabel)
				action, hasAction := ui.Find(actionLabel)
				if !hasPanel || !hasTitle || !hasAction {
					t.Fatalf("missing card content: %v", ui.Texts())
				}
				img := ui.Image()
				const scale = 2
				bg := img.RGBAAt(int((panel.X+8)*scale), int((panel.Y+20)*scale))
				above, below := -1.0, -1.0
				top, bottom := int(panel.Y*scale), int((panel.Y+panel.H)*scale)
				for y := int(title.Y * scale); y < int((title.Y+title.H)*scale) && above < 0; y++ {
					for x := int(title.X * scale); x < int((title.X+min(85, title.W))*scale); x++ {
						if delta := int(img.RGBAAt(x, y).R) - int(bg.R); delta < -55 || delta > 55 {
							above = float64(y-top) / scale
							break
						}
					}
				}
				x := int((action.X + action.W/2) * scale)
				for y := bottom - 3; y > int((action.Y+action.H)*scale); y-- {
					if img.RGBAAt(x, y) != bg {
						below = float64(bottom-y-1) / scale
						break
					}
				}
				t.Logf("visible upper clearance %.1f DIP; button clearance %.1f DIP", above, below)
				if above < 0 || below < 0 || math.Abs(above-below) > 1 {
					t.Fatal("the visible title and action should have balanced top and bottom clearance")
				}
			})
		}
	}
}

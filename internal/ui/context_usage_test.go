package ui

import (
	"bytes"
	n "github.com/egoist/mygo/ui"
	"image"
	"shioji/internal/domain"
	"testing"
	"time"
)

func contextReport(meterUsed, meterLimit int64) *domain.ContextUsage {
	return &domain.ContextUsage{TurnID: "turn", UsedTokens: &meterUsed, LimitTokens: &meterLimit, UpdatedAt: time.Now()}
}
func contextPixels(ui *n.Tester, r n.Rect) []byte {
	im := ui.Image()
	var out []byte
	b := image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)).Intersect(im.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := im.RGBAAt(x, y)
			out = append(out, p.R, p.G, p.B)
		}
	}
	return out
}

func hoverContextMeter(t *testing.T, ui *n.Tester) {
	t.Helper()
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	meter, ok := ui.Find("上下文用量")
	if !ok {
		t.Fatal("context meter missing")
	}
	ui.Move(meter.X+meter.W/2, meter.Y+meter.H/2)
}

func TestContextDetailsFollowHoverWithoutTakingComposerFocus(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions[0].ContextUsage = contextReport(7000, 100000)
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.Click("消息输入")
	ui.Type("keep draft")
	meter, _ := ui.Find("上下文用量")
	model, _ := ui.Find("运行配置")
	if meter.X+meter.W > model.X || model.X-(meter.X+meter.W) != 6 {
		t.Fatal("context ring must precede model with the shared gap", meter, model)
	}
	ui.Move(meter.X+meter.W/2, meter.Y+meter.H/2)
	ui.Advance(200 * time.Millisecond)
	panel, ok := ui.Find("上下文详情")
	if !ok {
		t.Fatal("hover did not reveal context details")
	}
	if !ui.Focused("消息输入") {
		t.Fatal("hover stole composer focus")
	}
	ui.Move(meter.X+meter.W/2, meter.Y-3)
	ui.Advance(40 * time.Millisecond)
	ui.Move(panel.X+panel.W/2, panel.Y+panel.H/2)
	ui.Advance(300 * time.Millisecond)
	if _, ok := ui.Find("上下文详情"); !ok {
		t.Fatal("details closed while crossing the gap into the panel")
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	if _, ok := ui.Find("上下文详情"); ok {
		t.Fatal("details remained pinned after pointer left")
	}
	if !ui.Focused("消息输入") || m.Drafts[m.DraftKey()] != "keep draft" {
		t.Fatal("hover changed focus or draft")
	}
}

func TestContextMeterUpdatesWithoutMovingComposerOrLosingDraft(t *testing.T) {
	for _, width := range []int{1240, 850} {
		for _, dark := range []bool{false, true} {
			m := layoutModel()
			m.Snapshot.Sessions[0].ContextUsage = contextReport(25000, 100000)
			ui := n.NewTester(New(m, nil, "").View, width, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			if _, ok := ui.Find("上下文用量"); !ok {
				t.Fatal("context meter missing")
			}
			ui.Click("消息输入")
			ui.Type("keep draft")
			ui.Advance(time.Second)
			dock, _ := ui.Find("消息编辑器")
			meter, _ := ui.Find("上下文用量")
			ring, ok := ui.Find("上下文圆环")
			if !ok {
				t.Fatal("context ring missing")
			}
			before := contextPixels(ui, ring)
			m.Snapshot.Sessions[0].ContextUsage = contextReport(75000, 100000)
			ui.Frame()
			first := contextPixels(ui, ring)
			ui.Advance(200 * time.Millisecond)
			middle := contextPixels(ui, ring)
			ui.Advance(time.Second)
			last := contextPixels(ui, ring)
			if !bytes.Equal(before, first) || bytes.Equal(first, middle) || bytes.Equal(middle, last) {
				t.Fatal("context ring skipped interpolation", width, dark)
			}
			end, _ := ui.Find("消息编辑器")
			after, _ := ui.Find("上下文用量")
			if dock != end || meter != after || !ui.Focused("消息输入") || m.Drafts[m.DraftKey()] != "keep draft" {
				t.Fatal("usage update moved composer or stole draft/focus")
			}
			hoverContextMeter(t, ui)
			ui.Advance(40 * time.Millisecond)
			entering, _ := ui.Find("上下文详情")
			ui.Advance(200 * time.Millisecond)
			settled, _ := ui.Find("上下文详情")
			if entering.W >= settled.W || settled.Y+settled.H > meter.Y+1 {
				t.Fatal("context popover did not expand above its trigger", entering, settled, meter)
			}
			if settled.X+settled.W > meter.X+meter.W+1 {
				t.Fatal("context popover escaped the composer instead of aligning to its trigger", settled, meter)
			}
			for _, text := range []string{"已用", "剩余", "上限", "75,000", "25,000", "100,000"} {
				if _, ok := ui.Find(text); !ok {
					t.Fatal("missing context detail", text, ui.Texts())
				}
			}
		}
	}
}

func TestContextMeterKeepsUnknownValuesAndStopsCompactingOnTurnEnd(t *testing.T) {
	m := layoutModel()
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("上下文用量"); !ok {
		t.Fatal("unknown context meter missing")
	}
	hoverContextMeter(t, ui)
	ui.Advance(time.Second)
	if _, ok := ui.Find("尚未报告上下文用量"); !ok {
		t.Fatal("unknown context presented as zero", ui.Texts())
	}
	m.Snapshot.Sessions[0].Status = "running"
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	d.Items = append(d.Items, domain.Item{ID: "compact", TurnID: "turn", Kind: "activity", Presentation: "compaction", Status: "running"})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("压缩中"); !ok {
		t.Fatal("active compaction not reflected in composer")
	}
	dock, _ := ui.Find("消息编辑器")
	d.Turns[0].Status = "interrupted"
	m.Details["s"] = d
	m.Snapshot.Sessions[0].Status = "interrupted"
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("压缩中"); ok {
		t.Fatal("ended turn left compaction spinner running")
	}
	end, _ := ui.Find("消息编辑器")
	if dock != end {
		t.Fatal("compaction status moved composer")
	}
	m.Snapshot.Sessions[0].ContextUsage = contextReport(25000, 100000)
	m.Snapshot.Sessions[0].ContextUsage.LimitTokens = nil
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("上限未知"); !ok {
		t.Fatal("unknown limit invented a percentage", ui.Texts())
	}
}

func TestOpenContextPopoverKeepsItsHeightDuringCompactionAndUnknownUpdates(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	hoverContextMeter(t, ui)
	ui.Advance(time.Second)
	before, _ := ui.Find("上下文详情")
	m.Snapshot.Sessions[0].ContextUsage = contextReport(25000, 100000)
	ui.Frame()
	ui.Advance(time.Second)
	known, _ := ui.Find("上下文详情")
	if before.H != known.H {
		t.Fatal("new context data jumped open popover height", before, known)
	}
	m.Snapshot.Sessions[0].Status = "running"
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	d.Items = append(d.Items, domain.Item{ID: "compact", TurnID: "turn", Kind: "activity", Presentation: "compaction", Status: "running"})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	active, _ := ui.Find("上下文详情")
	if known.H != active.H {
		t.Fatal("compaction state jumped open popover height", known, active)
	}
}

func TestWorkspaceContextActivityDoesNotStartCompactionSpinner(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions[0].Status = "running"
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	d.Items = append(d.Items, domain.Item{ID: "hook", TurnID: "turn", Kind: "activity", Presentation: "context", Status: "running", Name: "已补充工作区上下文"})
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("压缩中"); ok {
		t.Fatal("workspace context activity was treated as compaction")
	}
}

func TestContextRingStaysCompactAcrossStates(t *testing.T) {
	for _, width := range []int{850, 1240} {
		m := layoutModel()
		ui := n.NewTester(New(m, nil, "").View, width, 820)
		var previousRing, previousSend n.Rect
		for i, label := range []string{"—", "7%", "100%", "压缩中"} {
			if i == 1 {
				m.Snapshot.Sessions[0].ContextUsage = contextReport(7000, 100000)
			}
			if i == 2 {
				m.Snapshot.Sessions[0].ContextUsage = contextReport(100000, 100000)
			}
			if i == 3 {
				m.Snapshot.Sessions[0].Status = "running"
				d := m.Details["s"]
				d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
				d.Items = append(d.Items, domain.Item{ID: "compact", TurnID: "turn", Kind: "activity", Presentation: "compaction", Status: "running"})
				m.Details["s"] = d
			}
			ui.Frame()
			ui.Move(0, 0)
			ui.Advance(time.Second)
			if _, ok := ui.Find(label); ok {
				t.Fatal("context label is exposed outside details", label)
			}
			send, ok := ui.Find("发送")
			if !ok {
				send, ok = ui.Find("停止当前轮")
			}
			if !ok {
				t.Fatal("primary send/stop button missing")
			}
			ring, _ := ui.Find("上下文圆环")
			trigger, _ := ui.Find("上下文用量")
			if trigger.W != 28 || ring.W != 18 {
				t.Fatal("context trigger is not a compact icon button", trigger, ring)
			}
			if i > 0 && (ring != previousRing || send != previousSend) {
				t.Fatal("state change moved ring or send button", label, ring, send)
			}
			previousRing, previousSend = ring, send
			model, _ := ui.Find("运行配置")
			if model.X-(trigger.X+trigger.W) != 6 || send.X-(model.X+model.W) != 6 {
				t.Fatal("context/model/send spacing differs", width, trigger, model, send)
			}
			hoverContextMeter(t, ui)
			ui.Advance(time.Second)
			if _, ok := ui.Find(label); !ok {
				t.Fatal("context details lost their state", label)
			}
			ui.Key(0, n.KeyEscape)
			ui.Advance(time.Second)
		}
	}
}

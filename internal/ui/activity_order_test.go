package ui

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Display order and result delivery order must not decide activity ownership.
func TestLatestStartedActivityOwnsProgress(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, toolFinal := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/toolFinal=%v", dark, toolFinal), func(t *testing.T) {
				v := liveCompletionModel()
				d := v.Model.Details["s"]
				start := time.Now().Add(-108 * time.Second)
				d.Turns[0].StartedAt = &start
				d.Items = nil
				data := fmt.Sprintf(`[
					{"id":"compact","turnId":"t","kind":"activity","presentation":"compaction","name":"正在压缩上下文","status":"running","activityOrder":20},
					{"id":"tool","turnId":"t","kind":"tool","presentation":"command","name":"检查结果","status":"running","final":%v,"activityOrder":10}
				]`, toolFinal)
				if err := json.Unmarshal([]byte(data), &d.Items); err != nil {
					t.Fatal(err)
				}
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("activity-order-dark-%v-final-%v.png", dark, toolFinal))
				if _, ok := ui.Find("工具组运行状态"); ok {
					t.Error("older tool result reclaimed live progress")
				}
				compact, ok := ui.Find("正在压缩上下文")
				if !ok {
					t.Fatal("current compression missing")
				}
				clock, ok := ui.Find("运行时长")
				if !ok || clock.Y != compact.Y {
					t.Error("latest-started compression does not own elapsed time", compact, clock)
				}
				tools, ok := ui.Find("1 次工具调用")
				if !ok || tools.Y < compact.Y+compact.H {
					t.Error("live compaction moved from its recorded transcript position", compact, tools)
				}
				count := 0
				for _, label := range ui.Texts() {
					if label == "进行中状态" {
						count++
					}
				}
				if count != 1 {
					t.Error("transcript must have one progress dot", count)
				}
				// A genuine new start takes over, unlike an update to the old tool.
				var next domain.Item
				if err := json.Unmarshal([]byte(`{"id":"next","turnId":"t","kind":"tool","presentation":"command","name":"下一步","activityOrder":30}`), &next); err != nil {
					t.Fatal(err)
				}
				d.Items = append(d.Items, next)
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				if _, ok := ui.Find("工具组运行状态"); !ok {
					t.Error("new start did not take over progress")
				}
				count = 0
				for _, label := range ui.Texts() {
					if label == "进行中状态" {
						count++
					}
				}
				if count != 1 {
					t.Error("superseded compression kept a live dot", count)
				}
			})
		}
	}
}

func TestActivityOwnershipAcrossPhasesAndLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, kind, presentation, text, phase string
	}{
		{"thinking", "activity", "reasoning", "", "正在思考"},
		{"reply", "assistant", "", "正在整理结果", "正在回复"},
		{"command", "tool", "command", "输出", "正在执行命令"},
		{"tool", "tool", "mcp", "输出", "正在执行工具"},
		{"compaction", "activity", "compaction", "", "正在压缩上下文"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			start := time.Now().Add(-time.Minute)
			d.Turns[0].StartedAt = &start
			d.Items = []domain.Item{
				{ID: "old-compact", TurnID: "t", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", ActivityOrder: 5},
				{ID: "current", TurnID: "t", Kind: tc.kind, Presentation: tc.presentation, Text: tc.text, Name: tc.phase, ActivityOrder: 20},
				{ID: "old-result", TurnID: "t", Kind: "tool", Presentation: "command", Name: "较早工具的结果", Final: true, ActivityOrder: 10},
				{ID: "child", WorkID: "child", TurnID: "t", Kind: "tool", ActivityOrder: 30},
				{ID: "old-turn", TurnID: "old", Kind: "tool", ActivityOrder: 40},
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			check := func(dots int) {
				t.Helper()
				ui.Frame()
				ui.Advance(time.Second)
				count := 0
				for _, text := range ui.Texts() {
					if text == "进行中状态" {
						count++
					}
				}
				if count != dots {
					t.Fatalf("incorrect live progress count: got %d want %d", count, dots)
				}
			}
			check(1)
			dot, found := ui.Find("进行中状态")
			if !found {
				t.Fatal("missing tail progress")
			}
			if tc.name == "compaction" {
				group, ok := ui.Find("1 次工具调用")
				if !ok || group.Y < dot.Y+dot.H {
					t.Fatalf("the active compression moved below later transcript content: %v %v", dot, group)
				}
			} else {
				for key, bounds := range v.historyRowBounds {
					if key != "turn-footer:t" && bounds.H > 0 && bounds.Y > dot.Y+1 {
						t.Fatalf("content %s remains below progress: %v %v", key, bounds, dot)
					}
				}
			}
			if a := v.runningActivity(d.Turns[0]); a.item != "current" {
				t.Fatal("old result, child or previous turn took over", a)
			}
			if !ui.HasText(tc.phase) {
				t.Fatal("current phase not rendered", ui.Texts())
			}
			// Neither a partial output nor a final result is a new start.
			for _, final := range []bool{false, true} {
				d.Items[2].Final, d.Items[2].Text = final, "迟到的结果更新"
				v.Model.Details["s"] = d
				check(1)
				if a := v.runningActivity(d.Turns[0]); a.item != "current" {
					t.Fatal("result update changed owner", a)
				}
			}
			// A finished newest activity must not reactivate older unfinished items.
			d.Items[1].Final = true
			v.Model.Details["s"] = d
			check(1)
			if v.currentCompaction() != "" {
				t.Fatal("completion revived old compaction")
			}
			d.Requests = []domain.PendingRequest{{ID: "request", TurnID: "t", Kind: "approval", State: "pending", Title: "需要授权"}}
			v.Model.Details["s"] = d
			check(0)
			d.Requests = nil
			v.Model.Details["s"] = d
			v.Model.Connected = false
			check(0)
			if clock, ok := ui.Find("运行时长"); ok && clock.W > 0 && clock.H > 0 {
				t.Fatal("offline activity retained visible clock", clock)
			}
			v.Model.Connected = true
			d.Turns[0].Status = domain.ExecutionCompleted
			d.Session.Status = domain.ExecutionCompleted
			v.Model.Snapshot.Sessions[0].Status = domain.ExecutionCompleted
			v.Model.Details["s"] = d
			check(0)
		})
	}
}

func TestLateResultGroupKeepsProgressBelowAllContent(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{
		{ID: "first", TurnID: "t", Kind: "tool", Name: "当前组第一条", ActivityOrder: 15, Final: true},
		{ID: "current", TurnID: "t", Kind: "tool", Name: "当前组第二条", ActivityOrder: 20},
		{ID: "old-reply", TurnID: "t", Kind: "assistant", Text: "较早回复", ActivityOrder: 5, Final: true},
		{ID: "old-result", TurnID: "t", Kind: "tool", Name: "较早工具的迟到结果", ActivityOrder: 10, Final: true},
	}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("工具组运行状态"); ok {
		t.Fatal("an earlier tool group retained progress above newer content")
	}
	owner, ok := ui.Find("进行中状态")
	group, found := ui.Find("1 次工具调用")
	if !ok || !found || owner.Y < group.Y+group.H {
		t.Fatal("progress must follow the final result group", owner, group)
	}
}

func TestHiddenActivityKeepsProgressInTailToolSummary(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			v := toolTailFixture(10)
			m := v.Model
			m.Preferences.CollapseLatestTools = true
			d := m.Details["s"]
			start := time.Now().Add(-58 * time.Second)
			d.Turns[0].StartedAt = &start
			for i := range d.Items {
				d.Items[i].Final = true
				d.Items[i].Order = int64(i + 1)
				if d.Items[i].Kind == "tool" {
					d.Items[i].ActivityOrder = int64(i)
					d.Items[i].Status = "completed"
				}
			}
			m.Sequence = 10
			m.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(400 * time.Millisecond)
			upsert := func(item domain.Item) {
				t.Helper()
				if err := m.ApplyEvent(domain.Event{Type: "item.upsert", SessionID: "s", Sequence: m.Sequence + 1, Item: &item}); err != nil {
					t.Fatal(err)
				}
				ui.Frame()
			}
			check := func(count, phase string) {
				t.Helper()
				summary, summaryOK := ui.Find(count)
				caption, captionOK := ui.Find(phase)
				clock, clockOK := ui.Find("运行时长")
				_, grouped := ui.Find("工具组运行状态")
				if !grouped || !summaryOK || !captionOK || !clockOK || summary.Y != clock.Y || summary.Y != caption.Y {
					t.Errorf("计数、%s和计时应在同一行: %v %v %v", phase, summary, caption, clock)
				}
			}
			check("10 次工具调用", "进行中")
			reasoning := domain.Item{ID: "reason", TurnID: "t", Kind: "activity", Presentation: "reasoning", ActivityOrder: 11, Order: 12}
			upsert(reasoning)
			ui.Advance(300 * time.Millisecond)
			check("10 次工具调用", "正在思考")
			reasoning.Final = true
			upsert(reasoning)
			ui.Advance(400 * time.Millisecond)
			saveNativeSkillImage(t, ui, fmt.Sprintf("10-calls-dark-%v.png", dark))
			check("10 次工具调用", "正在思考")
			for i := int64(13); i <= 14; i++ {
				upsert(domain.Item{ID: fmt.Sprint("next-", i), TurnID: "t", Kind: "tool", Presentation: "command", Name: "检查结果", Order: i, ActivityOrder: i, Final: true})
			}
			ui.Advance(8 * time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("12-calls-dark-%v.png", dark))
			check("12 次工具调用", "进行中")
			reply := domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Order: 15, ActivityOrder: 15}
			upsert(reply)
			ui.Advance(300 * time.Millisecond)
			check("12 次工具调用", "正在回复")
			reply.Text = "检查结果已整理。"
			upsert(reply)
			ui.Advance(time.Second)
			body, bodyOK := ui.Find(reply.Text)
			footer, footerOK := ui.Find("回复信息")
			if !bodyOK || !footerOK || footer.Y < body.Y+body.H || !activityGreenIn(ui, footer) {
				t.Fatal("正文出现后，运行状态应交接到正文下方", body, footer)
			}
		})
	}
}

func TestNewReplyImmediatelyMovesProgressBelowContent(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprintf("expanded=%v", expanded), func(t *testing.T) {
			v := toolTailFixture(2)
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			if expanded {
				if err := ui.Click("2 次工具调用"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "新的回复"})
			v.Model.Details["s"] = d
			ui.Frame()
			for frame := 0; frame < 36; frame++ {
				ui.Advance(time.Second / 60)
				if v.runningGroup != "" {
					t.Fatalf("old tool group kept progress above new reply at frame %d", frame)
				}
				count := 0
				for _, text := range ui.Texts() {
					if text == "进行中状态" {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("expected one tail indicator at frame %d, got %d", frame, count)
				}
				if body, found := ui.Find("新的回复"); found {
					dot, ok := ui.Find("进行中状态")
					if !ok || dot.Y < body.Y+body.H {
						t.Fatalf("progress above new reply at frame %d: %v %v", frame, dot, body)
					}
				}
			}
		})
	}
}

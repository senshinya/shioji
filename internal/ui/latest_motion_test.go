package ui

import (
	"fmt"
	"image"
	"math"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func latestMotionFixture() (*View, *n.Tester) {
	v := toolTailFixture(1)
	state := &n.ListState{}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
			dock := n.Column(c.Key("dock")).Absolute().Left(40).Right(40).Bottom(16).Height(108).Radius(14).Background(c.Theme().Surface).Label("会话底部面板")
			v.returnToLatest(c, c.Theme(), state, dock)
		})
	}, 800, 260)
	ui.SetDark(false)
	ui.Advance(time.Second)
	return v, ui
}

func latestMotionPhase(v *View, phase string) {
	d := v.Model.Details["s"]
	i := &d.Items[len(d.Items)-1]
	i.Kind, i.Presentation = "tool", "command"
	i.Final = phase == "done"
	d.Turns[0].Status = "running"
	d.Session.Status = "running"
	d.Requests = nil
	v.Model.Connected = phase != "offline"
	switch phase {
	case "compaction":
		i.Kind, i.Presentation = "activity", "compaction"
	case "tool":
		i.Presentation = "other"
	case "thinking":
		i.Kind, i.Presentation = "activity", "reasoning"
	case "reply":
		i.Kind, i.Text = "assistant", "正在整理结果"
	case "waiting":
		d.Requests = []domain.PendingRequest{{ID: "pending", TurnID: d.Turns[0].ID, Kind: "questions", State: "pending"}}
	case "done":
		d.Turns[0].Status = "completed"
		d.Session.Status = "completed"
	}
	for index := range v.Model.Snapshot.Sessions {
		if v.Model.Snapshot.Sessions[index].ID == d.Session.ID {
			v.Model.Snapshot.Sessions[index].Status = d.Session.Status
		}
	}
	v.Model.Details["s"] = d
}

func TestLatestControlRapidChangesAndReduceMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			v, ui := latestMotionFixture()
			ui.SetDark(dark)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			for _, phase := range []string{"compaction", "offline", "command", "done", "tool", "waiting", "reply"} {
				ui.Frame() // Sample the in-flight width at the exact retarget time.
				before, _ := ui.Find("回到最新")
				latestMotionPhase(v, phase)
				ui.Frame()
				first, _ := ui.Find("回到最新")
				if !reduced && math.Abs(float64(first.W-before.W)) > 1 {
					t.Fatal("rapid reversal restarts from another width", phase, before, first)
				}
				for frame := 0; frame < 3; frame++ {
					ui.Advance(20 * time.Millisecond)
					r, _ := ui.Find("回到最新")
					if math.Abs(float64(r.X+r.W/2-400)) > .5 || r.H != 28 {
						t.Fatal("rapid/reduced transition loses capsule alignment", phase, r)
					}
					if reduced && math.Abs(float64(r.W-first.W)) > .5 {
						t.Fatal("Reduce Motion still animates width", phase, first, r)
					}
				}
			}
			ui.Advance(time.Second)
			if !ui.HasText("正在回复") || ui.HasText("最新消息") || ui.HasText("状态待确认") {
				t.Fatal("rapid changes leave stale status content", ui.Texts())
			}
		}
	}
}

func TestLatestControlAnimatesClockWidthAtMinuteBoundary(t *testing.T) {
	v, ui := latestMotionFixture()
	d := v.Model.Details["s"]
	started := ui.Now().Add(-59 * time.Second)
	d.Turns[0].StartedAt = &started
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	ui.Advance(690 * time.Millisecond)
	ui.Frame()
	before, _ := ui.Find("回到最新")
	ui.Advance(10 * time.Millisecond)
	ui.Frame()
	first, _ := ui.Find("回到最新")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("回到最新")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("回到最新")
	if math.Abs(float64(first.W-before.W)) > 1 || middle.W <= before.W+1 || middle.W >= end.W-1 {
		t.Fatal("seconds-to-minutes change snaps the latest width", before, first, middle, end)
	}
}

func TestLatestControlNativeMotionRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_LATEST_RECORDING")
	if dir == "" {
		t.Skip("opt-in native latest control recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v := toolTailFixture(3)
	d := v.Model.Details["s"]
	var history []domain.Item
	for i := 0; i < 18; i++ {
		history = append(history,
			domain.Item{ID: fmt.Sprint("earlier-question-", i), TurnID: "earlier", Kind: "user", Text: "检查会话的运行状态和消息位置", Final: true},
			domain.Item{ID: fmt.Sprint("earlier-answer-", i), TurnID: "earlier", Kind: "assistant", Text: "阅读历史消息时，可以通过发送框上方的状态指示器了解当前进度。\n\n状态变化时，文案和宽度同步过渡；点击指示器可以回到最新内容。", Final: true})
	}
	d.Items = append(history, d.Items...)
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	ui.Scroll(1000, 300, 0, -900)
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("回到最新"); !ok {
		t.Fatal("recording must show the real floating latest control")
	}
	for frame := 0; frame < 480; frame++ {
		switch frame {
		case 45:
			latestMotionPhase(v, "tool")
		case 105:
			latestMotionPhase(v, "compaction")
		case 165:
			latestMotionPhase(v, "thinking")
		case 225:
			latestMotionPhase(v, "reply")
		case 285:
			latestMotionPhase(v, "done")
		case 345:
			latestMotionPhase(v, "command")
		case 390:
			latestMotionPhase(v, "compaction")
		case 392:
			latestMotionPhase(v, "tool")
		case 394:
			latestMotionPhase(v, "thinking")
		case 420:
			ui.SetPreferences(n.Preferences{ReduceMotion: true})
			latestMotionPhase(v, "compaction")
		}
		ui.Frame()
		ui.Advance(time.Second / 30)
		if _, ok := ui.Find("回到最新"); !ok {
			t.Fatalf("latest indicator disappeared during recording at frame %d", frame)
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}

func TestLatestControlInterpolatesWidthAndStaysCentered(t *testing.T) {
	v, ui := latestMotionFixture()
	before, _ := ui.Find("回到最新")
	beforeLabel, _ := ui.Find("正在执行命令")
	saveNativeSkillImage(t, ui, "before-command.png")
	latestMotionPhase(v, "compaction")
	ui.Frame()
	first, _ := ui.Find("回到最新")
	firstLabel, _ := ui.Find("正在压缩上下文")
	saveNativeSkillImage(t, ui, "before-change.png")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("回到最新")
	ui.Advance(time.Second)
	end, _ := ui.Find("回到最新")
	if math.Abs(float64(first.W-before.W)) > 1 || middle.W <= before.W+1 || middle.W >= end.W-1 {
		t.Fatal("latest width jumps instead of interpolating", before, first, middle, end)
	}
	if math.Abs(float64(firstLabel.X-beforeLabel.X)) > 1 {
		t.Fatal("status words jump horizontally before the width animation starts", beforeLabel, firstLabel)
	}
	for _, r := range []n.Rect{first, middle, end} {
		if math.Abs(float64(r.X+r.W/2-400)) > .5 || r.H != 28 {
			t.Fatal("animated latest control moves away from its dock center", r)
		}
	}
	// Completion removes the clock and status dot, through the same width clock.
	latestMotionPhase(v, "done")
	ui.Frame()
	first, _ = ui.Find("回到最新")
	ui.Advance(60 * time.Millisecond)
	middle, _ = ui.Find("回到最新")
	ui.Advance(time.Second)
	done, _ := ui.Find("回到最新")
	if math.Abs(float64(first.W-end.W)) > 1 || middle.W >= end.W-1 || middle.W <= done.W+1 || !ui.HasText("最新消息") {
		t.Fatal("completion snaps to the ordinary latest pill", end, first, middle, done)
	}
}

func latestTextDifference(a, b *image.RGBA, r n.Rect) int64 {
	var sum int64
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			p, q := a.RGBAAt(x, y), b.RGBAAt(x, y)
			sum += int64(math.Abs(float64(int(p.R) - int(q.R))))
		}
	}
	return sum
}

func TestLatestControlCrossfadesChangedWords(t *testing.T) {
	v, ui := latestMotionFixture()
	label, _ := ui.Find("正在执行命令")
	before := ui.Image()
	latestMotionPhase(v, "tool") // Same width, only the final two words change.
	ui.Frame()
	first := latestTextDifference(before, ui.Image(), label)
	ui.Advance(80 * time.Millisecond)
	middleImage := ui.Image()
	middle := latestTextDifference(before, middleImage, label)
	ui.Advance(time.Second)
	end := latestTextDifference(before, ui.Image(), label)
	if end < 1000 || first > end/10 || middle <= first+100 || latestTextDifference(middleImage, ui.Image(), label) < 100 {
		t.Fatal("latest status text replaces immediately instead of crossfading", first, middle, end)
	}
}

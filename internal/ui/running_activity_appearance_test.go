package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Evidence uses the production MyGo View and renderer with explicit normalized
// event fixtures, not a browser reconstruction or a simulated model response.
func TestRunningActivityNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_ACTIVITY_CAPTURE")
	if dir == "" {
		t.Skip("set SHIOJI_ACTIVITY_CAPTURE")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		v := liveCompletionModel()
		m := v.Model
		m.WorkerMinor = 30
		m.Snapshot.Workspaces[0].Name = "红黑树"
		m.Snapshot.Sessions[0].Title = "红黑树旋转"
		m.Readiness.Models = []domain.Model{{ID: "gpt-6.1-sol", Default: true}}
		d := m.Details["s"]
		d.Session.Title = "红黑树旋转"
		prefix := []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "讲一下红黑树的左旋和右旋，给一个简单的 Go 示例。", Final: true}, {ID: "body", TurnID: "t", Kind: "assistant", Text: "旋转只改变局部的连接关系，节点的大小顺序保持不变。\n\n左旋时，右孩子顶上来，原节点向左下方移动。右旋则把这个过程反过来。", Final: true}}
		d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "thinking", TurnID: "t", Kind: "activity", Presentation: "reasoning"})
		m.Details["s"] = d
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		started := ui.Now().Add(-35 * time.Second)
		d.Turns[0].StartedAt = &started
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		save := func(name, label string) {
			t.Helper()
			ui.Frame()
			ui.Advance(500 * time.Millisecond)
			saveNativeSkillImage(t, ui, theme+"-"+name+".png")
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("capture target missing", label, ui.Texts())
			}
			if name == "compact" {
				r.X -= 12
			}
			bounds := image.Rect(int(r.X), int(r.Y)-6, int(r.X)+736, int(r.Y+r.H)+8).Intersect(ui.Image().Bounds())
			im := ui.Image().SubImage(bounds)
			f, err := os.Create(filepath.Join(dir, theme+"-"+name+"-detail.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err = png.Encode(f, im); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		save("thinking", "回复信息")
		d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test ./...", Text: "ok   tree/rotate   0.028s", Final: true, Status: "completed"}, domain.Item{ID: "b", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test -race ./...", Status: "running"})
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		save("expanded", "回复信息")
		ui.Click("2 次工具调用")
		ui.Advance(time.Second)
		save("folded", "2 次工具调用")
		d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "source", TurnID: "t", Kind: "request", Text: "需要回答", Status: "pending"})
		d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", TurnID: "t", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q1", Text: "你希望示例使用指针，还是数组索引？", Custom: true, Options: []domain.Option{{Label: "指针"}, {Label: "数组索引"}}}}}}
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		save("waiting", "回复信息")
		d.Requests = nil
		d.Turns[0].Presentation = "compaction"
		d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "compact-command", TurnID: "t", Kind: "command", Presentation: "compact", Name: "压缩上下文", Final: true}, domain.Item{ID: "compacting", TurnID: "t", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文"})
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		save("compact", "正在压缩上下文")
		saveNativeSkillImage(t, ui, theme+"-compact-icon.png")
		// A short capture of the actual native disclosure and status transitions.
		if !dark && os.Getenv("SHIOJI_ACTIVITY_MOTION") != "" {
			d.Turns[0].Presentation = ""
			d.Items = prefix
			d.Requests = nil
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			for frame := 0; frame < 150; frame++ {
				switch frame {
				case 20:
					d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "motion-tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test -race ./...", Status: "running"})
					m.Details["s"] = d
					ui.Frame()
				case 55:
					ui.Click("1 次工具调用")
				case 95:
					ui.Click("1 次工具调用")
				case 125:
					d.Items = append(append([]domain.Item{}, prefix...), domain.Item{ID: "motion-reply", TurnID: "t", Kind: "assistant", Text: "先把右孩子的左子树交给原节点。"})
					m.Details["s"] = d
					ui.Frame()
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				ui.Advance(time.Second / 60)
			}
		}
	}
}

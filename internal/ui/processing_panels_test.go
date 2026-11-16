package ui

import (
	n "github.com/egoist/mygo/ui"
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

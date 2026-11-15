package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestHistoryEdgeNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_HISTORY_EDGE_RECORDING")
	if dir == "" {
		t.Skip("opt-in native paging recording")
	}
	for _, newer := range []bool{false, true} {
		t.Run(fmt.Sprint("newer=", newer), func(t *testing.T) {
			name := "older"
			if newer {
				name = "newer"
			}
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			m := layoutModel()
			m.WorkerMinor = 37
			m.Preferences.Appearance = "light"
			v := New(m, app.NewController(""), "")
			d := m.Details["s"]
			d.Session.Title = "历史分页 · 连续阅读"
			d.Session.ThreadID = "edge-source"
			d.Session.Status = domain.ExecutionCompleted
			d.Turns = nil
			d.Items = nil
			m.Snapshot.Sessions[0] = d.Session
			page := func(first, last int) domain.SessionDetail {
				out := d
				out.Items = nil
				out.History = &domain.HistoryWindow{First: int64(first * 10), Last: int64(last*10 + 1), WindowFirst: int64(first * 10), WindowLast: int64(last*10 + 1), Older: first > 1, Newer: last < 119}
				for i := first; i <= last; i++ {
					out.Items = append(out.Items, domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Order: int64(i * 10), Text: fmt.Sprintf("第 %d 轮 · 检查连续阅读的位置", i), Final: true}, domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Order: int64(i*10 + 1), Text: fmt.Sprintf("第 %d 轮检查结果。\n\n继续向前阅读，历史会在接近边缘时后台加载。", i), Final: true})
				}
				return out
			}
			if newer {
				d = page(80, 99)
			} else {
				d = page(100, 119)
			}
			m.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(false)
			ui.Advance(time.Second)
			// Set up a page boundary, then record actual wheel/touchpad input and responses.
			if newer {
				v.lists["s"].ScrollToEnd()
			} else {
				v.lists["s"].ScrollTo(0, n.Start)
			}
			ui.Frame()
			ui.Advance(time.Second)
			marker := "u100"
			if newer {
				marker = "u99"
			}
			var pending *app.Job
			pendingAt := 0
			reads := 0
			for frame := 0; frame < 330; frame++ {
				if frame == 30 {
					dy := float32(-1)
					if newer {
						dy = 1
					}
					ui.Scroll(1000, 350, 0, dy)
				}
				if frame >= 100 && frame < 260 && frame%4 == 0 {
					dy := float32(-65)
					if newer {
						dy = 65
					}
					ui.Wheel(1000, 350, 0, dy)
				}
				ui.Advance(time.Second / 30)
				for len(v.Controller.Jobs) > 0 {
					job := <-v.Controller.Jobs
					if job.Method == "session.outline" {
						b, _ := json.Marshal(domain.HistoryOutline{SessionID: "s", ThreadID: d.Session.ThreadID})
						job.Apply(m, b, nil)
					}
					if job.Method == "session.history" {
						q := job.Input.(domain.HistoryQuery)
						if q.SummaryOnly {
							continue
						}
						if pending != nil {
							t.Fatal("overlapping page reads")
						}
						pending = &job
						pendingAt = frame
						reads++
					}
				}
				if pending != nil && frame-pendingAt >= 15 {
					ui.Frame() // Same clock on each side of the page arrival, including wheel inertia.
					before, visible := v.historyRowBounds[marker]
					q := pending.Input.(domain.HistoryQuery)
					var next domain.SessionDetail
					if q.Before > 0 {
						last := int((q.Before - 1) / 10)
						next = page(last-19, last)
					} else {
						first := int(q.After/10) + 1
						next = page(first, first+19)
					}
					b, _ := json.Marshal(next)
					pending.Apply(m, b, nil)
					pending = nil
					if v.historyPage.error != "" {
						t.Fatal(v.historyPage.error)
					}
					ui.Frame()
					after, present := v.historyRowBounds[marker]
					if visible && (!present || math.Abs(float64(after.Y-before.Y)) > 1) {
						t.Fatal("page arrival moved visible boundary", before, after)
					}
				}
				for _, label := range []string{"加载更早消息", "加载后续消息", "正在加载历史…"} {
					if ui.HasText(label) {
						t.Fatal("normal paging exposes loading UI", label)
					}
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
			if reads == 0 {
				t.Fatal("recording did not load a page")
			}
		})
	}
}

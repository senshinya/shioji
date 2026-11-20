package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestTranscriptDisclosureIgnoresBlankHeadingSpace(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, kind := range []string{"reasoning", "search", "command", "child-command"} {
			t.Run(fmt.Sprintf("%s/dark=%v", kind, dark), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				item := domain.Item{ID: "item", Kind: "tool", Presentation: kind, Name: "检查文件", Text: "详情正文", Status: "completed", Final: true}
				if kind == "reasoning" {
					item.Kind = "activity"
				}
				if kind == "command" || kind == "child-command" {
					item.Name, item.Presentation = "go test ./...", "command"
					v.Model.Collapsed[v.commandOutputKey(item.ID, "")] = true
				}
				if kind == "child-command" {
					v, _ = sessionWorkFixture(t)
					key := v.Model.WorkKey("s", "agent-a")
					d := v.Model.WorkDetails[key]
					d.Items = []domain.WorkItem{{Item: item}}
					v.Model.WorkDetails[key] = d
				}
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(24).Background(c.Theme().Background).Children(func() {
						if kind == "child-command" {
							v.sessionWorkDetail(c, c.Theme(), "agents", v.workPanel("agents"), v.Model.Details["s"].Works[0], 572)
						} else {
							v.item(c, c.Theme(), item)
						}
					})
				}, 620, 400)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if ui.HasText(item.Text) {
					t.Fatal("fixture did not start folded")
				}
				for _, open := range []bool{false, true} {
					heading, ok := ui.Find(item.Name)
					if !ok {
						t.Fatal("heading missing")
					}
					ui.ClickAt(560, heading.Y+heading.H/2)
					ui.Advance(250 * time.Millisecond)
					if ui.HasText(item.Text) != open {
						t.Fatalf("blank space toggled the disclosure: open=%v heading=%+v", open, heading)
					}
					if !open {
						// Click the title itself, then the trailing arrow to close.
						ui.ClickAt(heading.X+4, heading.Y+heading.H/2)
					} else {
						body, _ := ui.Find(item.Text)
						ui.ClickAt(body.X+4, body.Y+body.H/2)
						if !ui.HasText(item.Text) {
							t.Fatal("body click folded its heading")
						}
						ui.ClickAt(heading.X+heading.W-5, heading.Y+10)
					}
					ui.Advance(250 * time.Millisecond)
					if ui.HasText(item.Text) == open {
						t.Fatalf("title or arrow no longer toggles the disclosure: open=%v", open)
					}
				}
			})
		}
	}
}

func TestToolGroupDisclosureIgnoresBlankHeadingSpace(t *testing.T) {
	for _, ended := range []bool{false, true} {
		t.Run(fmt.Sprint("ended=", ended), func(t *testing.T) {
			v := toolTailFixture(2)
			if ended {
				d := v.Model.Details["s"]
				d.Turns[0].Status = "completed"
				d.Items[len(d.Items)-1].Status, d.Items[len(d.Items)-1].Final = "completed", true
				v.Model.Details["s"] = d
			}
			ui := n.NewTester(v.View, 1440, 900)
			ui.Advance(time.Second)
			const label = "2 次工具调用"
			if ended {
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			key := "tool-group:s/call-0"
			for _, closed := range []bool{false, true} {
				heading, ok := ui.Find(label)
				if !ok {
					t.Fatal("group heading missing", label)
				}
				ui.ClickAt(heading.X+400, heading.Y+10)
				ui.Advance(250 * time.Millisecond)
				if v.Model.ToolDisclosure(key).Closed() != closed {
					t.Fatalf("blank space toggled tool group: closed=%v heading=%+v", closed, heading)
				}
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(250 * time.Millisecond)
				if v.Model.ToolDisclosure(key).Closed() == closed {
					t.Fatal("group title no longer toggles its tools")
				}
			}
		})
	}
}

func TestQueueDisclosureIgnoresBlankHeadingSpace(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Advance(time.Second)
	for _, open := range []bool{false, true} {
		heading, _ := ui.Find("待发送 2")
		panel, _ := ui.Find("待发送队列")
		ui.ClickAt(panel.X+panel.W-24, heading.Y+heading.H/2)
		ui.Advance(250 * time.Millisecond)
		if v.Model.QueueExpanded["s"] != open {
			t.Fatalf("blank queue header toggled its contents: open=%v", open)
		}
		if err := ui.Click("待发送 2"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(250 * time.Millisecond)
		if v.Model.QueueExpanded["s"] == open {
			t.Fatal("queue title no longer toggles its contents")
		}
	}
}

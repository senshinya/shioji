package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"testing"
	"time"
)

func TestMarkdownContextMenuHitsExactCompletedLink(t *testing.T) {
	var chosen string
	options := markdown.Options{ShareSelection: true, LinkContextMenu: func(menu *n.Menu, dest string) {
		if menu.Item("Forward").Chosen() {
			chosen = dest
		}
	}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(20).Selectable().Children(func() {
			markdown.View(c, "[First](http://localhost:3000/a) and [Second](http://192.168.1.2:8080/b?q=1#part)", &options).FillWidth()
		})
	}, 500, 160)
	if err := ui.RightClick("Second"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("Forward"); err != nil {
		t.Fatal(err)
	}
	if chosen != "http://192.168.1.2:8080/b?q=1#part" {
		t.Fatal("menu used wrong link", chosen)
	}
}
func TestForwardLinkContextUsesOwningSessionAcrossMessageKinds(t *testing.T) {
	for _, kind := range []string{"assistant", "user", "tool"} {
		t.Run(kind, func(t *testing.T) {
			m := deviceUIModel()
			session := domain.Session{ID: "remote-session", WorkspaceID: "wb", Title: "远端预览", Status: "completed", Context: domain.LaunchContext{DeviceID: "B"}}
			m.ApplyDeviceUpdate("air", func(p *app.Model) {
				p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "b", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B", Name: "Remote"}}, Sessions: []domain.Session{session}})
			})
			const dest = "http://localhost:5173/a%2Fb?q=1#part"
			item := domain.Item{ID: "url", Kind: kind, Text: "[service](" + dest + ")", Final: true}
			label := "service"
			if kind == "user" {
				item.Text = "预览 " + dest
				label = dest
			}
			if kind == "tool" {
				item.Name = "开发服务器"
				item.Presentation = "mcp"
				item.Parts = []domain.ContentPart{{Kind: "markdown", Text: item.Text}}
			}
			m.SessionID = session.ID
			m.WorkspaceID = "wb"
			m.ActiveConnection = "air"
			m.RefreshDevices()
			m.Details[session.ID] = domain.SessionDetail{Session: session, Items: []domain.Item{item}}
			controller := app.NewController("")
			v := New(m, controller, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			if v.md.LinkContextMenu == nil {
				t.Fatalf("source binding missing: session=%+v workspace=%s device=%s", m.Details[m.SessionID].Session, m.WorkspaceConnection("wb"), m.ConnectionDeviceID("air"))
			}
			if kind == "tool" {
				if err := ui.Click("1 次工具调用"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(300 * time.Millisecond)
				if !ui.HasText("service") {
					if err := ui.Click("开发服务器"); err != nil {
						t.Fatal(err, ui.Texts())
					}
				}
				ui.Advance(300 * time.Millisecond)
			}
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("URL glyph missing", ui.Texts())
			}
			ui.RightClickAt(r.X+4, r.Y+r.H/2)

			if !slices.Contains(ui.Menu(), "端口转发并打开") {
				t.Fatal("missing forwarding context action", ui.Menu(), ui.Texts())
			}
			m.WorkspaceID = "w" // navigation intent cannot change the displayed session's owner.
			if err := ui.ChooseMenuItem("端口转发并打开"); err != nil {
				t.Fatal(err)
			}
			job := welcomeJob(t, controller, "$forward.create")
			in := job.Input.(app.ForwardInput)
			if job.ConnectionID != "air" || in.Request.DeviceID != "B" || in.URL != dest || in.OpenURL == nil || in.Request.LocalPort != 5173 || in.Request.Target.Port != 5173 || !in.Request.AutoPort {
				t.Fatal(fmt.Sprint(job.ConnectionID, in))
			}
			for len(controller.Jobs) < cap(controller.Jobs) {
				controller.Jobs <- app.Job{Method: "test-queued"}
			}
			m.Error = ""
			ui.RightClickAt(r.X+4, r.Y+r.H/2)
			if err := ui.ChooseMenuItem("端口转发并打开"); err != nil {
				t.Fatal(err)
			}
			if m.Error == "" {
				t.Fatal("full controller queue silently discarded forwarding")
			}

		})
	}
}

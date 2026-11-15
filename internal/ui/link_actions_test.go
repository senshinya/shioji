package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConversationFileLinkHasDirectSaveAndOpenActions(t *testing.T) {
	for _, action := range []string{"另存为…", "用默认应用打开"} {
		t.Run(action, func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items[0].Text = "[项目文档](docs/review.md:12)"
			d.Items[0].Final = true
			m.Details["s"] = d
			v := New(m, app.NewController(t.TempDir()), "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			r, _ := ui.Find("项目文档")
			ui.RightClickAt(r.X+4, r.Y+r.H/2)
			for _, label := range []string{"另存为…", "用默认应用打开", "复制链接"} {
				if !slices.Contains(ui.Menu(), label) {
					t.Fatal("missing link action", label, ui.Menu())
				}
			}
			if err := ui.ChooseMenuItem(action); err != nil {
				t.Fatal(err)
			}
			if v.fileLink == nil || v.fileLink.path != "docs/review.md" || v.fileLink.session != "s" || v.fileLink.workspace != "w" {
				t.Fatal("action lost file/session ownership", v.fileLink)
			}
		})
	}
}

func TestExplicitDefaultFileOpenUsesCachedTextAndNeverExecutableMIME(t *testing.T) {
	for _, name := range []string{"main.go", "notes.md", "payload.command", "payload.scpt", "payload.jar"} {
		t.Run(name, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			var services n.Services
			ui := n.NewTester(func(c *n.Context) { services = c.Services(); v.View(c) }, 1240, 820)
			s := &fileLinkState{action: fileLinkOpen, session: "s", workspace: "w", epoch: v.Model.ConnectionEpoch, path: "/device/" + name, services: services, result: &app.LinkedFile{Path: "/private/cache/" + name, Resource: domain.FileResource{Name: name, MIME: "text/plain"}}}
			v.fileLink = s
			v.openLinkedFile(s)
			ui.Frame()
			ui.Advance(time.Second)
			urls := ui.OpenedURLs()
			if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".md") {
				if len(urls) != 1 || urls[0] != "file:///private/cache/"+name {
					t.Fatal("did not open downloaded document", urls)
				}
			} else if len(urls) != 0 {
				t.Fatal("executed file through MIME spoof", urls)
			}
		})
	}
}

func TestLocalConversationWebLinksHaveContextActions(t *testing.T) {
	for _, kind := range []string{"assistant", "user"} {
		t.Run(kind, func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			text, label := "[网页](https://example.com/report.pdf)", "网页"
			if kind == "user" {
				text = "https://example.com/report.pdf"
				label = text
			}
			d.Items = []domain.Item{{ID: "link", Kind: kind, Text: text, Final: true, Status: "sent"}}
			m.Details["s"] = d
			ui := n.NewTester(New(m, app.NewController(t.TempDir()), "").View, 1240, 820)
			ui.Advance(time.Second)
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing link")
			}
			ui.RightClickAt(r.X+4, r.Y+r.H/2)
			if !slices.Contains(ui.Menu(), "另存为…") {
				t.Fatal("web link has no save action", ui.Menu())
			}
			if err := ui.ChooseMenuItem("用默认应用打开"); err != nil {
				t.Fatal(err)
			}
			urls := ui.OpenedURLs()
			if len(urls) != 1 || urls[0] != "https://example.com/report.pdf" {
				t.Fatal("did not open exact URL", urls)
			}
		})
	}
}

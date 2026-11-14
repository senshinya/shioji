package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestCommandTitleAndOutputStayInsideTheirColumn(t *testing.T) {
	samples := map[string]string{
		"unbroken":     strings.Repeat("very_long_identifier_without_any_spaces_", 120),
		"escaped-code": `s=s.replace('\\t\\tv.md.Theme = replyTheme(m.Preferences)','\\t\\tv.md.Theme = replyTheme(m.Preferences)\\n\\t\\tv.bindMarkdownLinks(c, &v.md)\\n\\t\\tif v.fileLink != nil && !v.fileLinkCurrent(v.fileLink) { v.cancelFileLink() }',1).replace('\\t\\tv.attachmentDialog(c, &t)','\\t\\tv.fileLinkDialog(c, &t)\\n\\t\\tv.attachmentDialog(c, &t)',1)`,
	}
	for _, dark := range []bool{false, true} {
		for name, output := range samples {
			t.Run(fmt.Sprintf("%s/dark=%v", name, dark), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				item := domain.Item{ID: "long", Kind: "tool", Presentation: "command", Name: "/bin/zsh -lc '" + strings.Repeat("long command argument ", 60) + "'", Text: output, Final: true, Status: "completed"}
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, v.Model.Preferences)
					n.Column(c).Fill().Background(theme.Background).Children(func() {
						n.Column(c).Width(420).Padding(12).Gap(8).Label("bounded command column").Children(func() { v.item(c, theme, item) })
					})
				}, 760, 300)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("bounds-%s-%v.png", name, dark))
				column, _ := ui.Find("bounded command column")
				heading, _ := ui.Find(item.Name)
				if heading.X+heading.W > column.X+column.W+.5 || heading.H > 40.5 {
					t.Errorf("long heading exceeds column: %v %v", column, heading)
				}
				im := ui.Image()
				background := im.RGBAAt(750, 280)
				spills := 0
				for y := 0; y < 260; y++ {
					for x := 409; x < 760; x++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, background.R), absChannel(p.G, background.G), absChannel(p.B, background.B)) > 30 {
							spills++
						}
					}
				}
				if spills != 0 {
					t.Errorf("command painted %d pixels outside its 420-DIP column", spills)
				}
			})
		}
	}
}

func TestLongCommandStaysInsideConversationComposerBaseline(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			d.Items = []domain.Item{{ID: "long-command", TurnID: "t", Kind: "tool", Presentation: "command", Name: "python3 - <<'PY'\n" + strings.Repeat("print('a very long command')\n", 50) + "PY", Text: strings.Repeat("s=s.replace('\\t\\tv.md.Theme=replyTheme(m.Preferences)','\\t\\tv.bindMarkdownLinks(c,&v.md)');", 100), Status: "running"}}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			frame, ok := ui.Find("消息编辑区")
			if !ok {
				t.Fatal("missing composer baseline")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("conversation-%v.png", dark))
			im := ui.Image()
			background := im.RGBAAt(1238, 300)
			spills := 0
			for y := 60; y < 300; y++ {
				for x := int(frame.X+frame.W) + 1; x < 1240; x++ {
					p := im.RGBAAt(x, y)
					if max(absChannel(p.R, background.R), absChannel(p.G, background.G), absChannel(p.B, background.B)) > 30 {
						spills++
					}
				}
			}
			if spills > 0 {
				t.Fatalf("long command paints %d pixels beyond composer right baseline %v", spills, frame)
			}
		})
	}
}

func TestChildAndBackgroundLongCommandsStayInsidePanel(t *testing.T) {
	for _, kind := range []string{"agents", "background"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%v", kind, dark), func(t *testing.T) {
				v, _ := sessionWorkFixture(t)
				index := 0
				if kind == "background" {
					index = 1
				}
				w := v.Model.Details["s"].Works[index]
				w.Brief = strings.Repeat("very_long_command_argument_", 100)
				key := v.Model.WorkKey("s", w.ID)
				d := v.Model.WorkDetails[key]
				d.Work = w
				d.Items = []domain.WorkItem{{Item: domain.Item{ID: "long-output", Kind: "tool", Presentation: "command", Name: strings.Repeat("long_command_", 100), Text: strings.Repeat("escaped_replace('\\t\\tfile.Path','replacement-value-without-breaks');", 100), Final: true}}}
				v.Model.WorkDetails[key] = d
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, v.Model.Preferences)
					n.Column(c).Fill().Background(theme.Background).Children(func() {
						n.Column(c).Width(420).FillHeight().Children(func() { v.sessionWorkDetail(c, theme, kind, v.workPanel(kind), w, 420) })
					})
				}, 760, 450)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("%s-%v.png", kind, dark))
				im := ui.Image()
				background := im.RGBAAt(750, 440)
				spills := 0
				for y := 0; y < 450; y++ {
					for x := 421; x < 760; x++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, background.R), absChannel(p.G, background.G), absChannel(p.B, background.B)) > 30 {
							spills++
						}
					}
				}
				if spills > 0 {
					t.Fatal("long command escapes sidebar", spills)
				}
			})
		}
	}
}

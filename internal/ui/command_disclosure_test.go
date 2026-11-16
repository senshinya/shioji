package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestNewCommandFoldsOlderOutputAndPreservesManualInspection(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "first", TurnID: "t", Kind: "tool", Presentation: "command", Name: "first command", Text: "first output", Status: "running"}, {ID: "second", TurnID: "t", Kind: "tool", Presentation: "command", Name: "second command", Text: "second output", Status: "running"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "commands-initial.png")
	if ui.HasText("first output") || ui.HasText("second output") {
		t.Error("commands do not start with collapsed outputs")
	}
	ui.Click("first command")
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("first output") {
		t.Error("older command cannot be inspected manually")
	}
	d.Items[0].Text += "\nlate first output"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("late first output") {
		t.Error("ordinary output update overrides manual inspection")
	}
	// Fold immediately on the new command, before it has any stdout.
	d.Items = append(d.Items, domain.Item{ID: "third", TurnID: "t", Kind: "tool", Presentation: "command", Name: "third command", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	saveNativeSkillImage(t, ui, "commands-new-empty.png")
	if ui.HasText("first output") || ui.HasText("second output") {
		t.Error("new command waits for stdout before folding previous outputs")
	}
	d.Items[2].Text = "third output"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if ui.HasText("third output") {
		t.Error("stdout opened the newest command automatically")
	}
	saveNativeSkillImage(t, ui, "commands-latest.png")
	ui.Click("third command")
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("third output") {
		t.Fatal("new command cannot be inspected manually")
	}
	ui.Click("third command")
	ui.Advance(250 * time.Millisecond)
	d.Items[2].Text += "\nmore third output"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if ui.HasText("third output") {
		t.Error("stdout reopens a manually folded command")
	}
	ui.Click("third command")
	d.Items = append(d.Items, domain.Item{ID: "mcp", TurnID: "t", Kind: "tool", Presentation: "mcp", Name: "other tool"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("third output") {
		t.Error("a different kind of tool is mistaken for a new command")
	}
}

func TestLongCommandHeadingUsesTwoLinesAndCopiesOriginal(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			command := "/bin/zsh -lc 'python3 - <<\"PY\"\n" + strings.Repeat("print(\"一条很长的多行命令，需要保留完整原文\")\n", 60) + "PY'"
			code := 1
			item := domain.Item{ID: "long", Kind: "tool", Presentation: "command", Name: command, Text: "output remains visible", Status: "failed", ExitCode: &code, Final: true}
			ui := n.NewTester(func(c *n.Context) {
				theme := applyAppearance(c, v.Model.Preferences)
				n.Column(c).Fill().Padding(20).Gap(8).Background(theme.Background).Children(func() { v.item(c, theme, item) })
			}, 620, 240)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("long-command-dark-%v.png", dark))
			heading, ok := ui.Find(command)
			issue, found := ui.Find("退出码 1")
			if !ok || heading.H <= 21 || heading.H > 40.5 || !found || issue.X+issue.W > 601 {
				t.Error("long command does not use two bounded lines or displaces its exit status", heading, issue)
			}
			output, shown := ui.Find("output remains visible")
			if !shown || output.Y+output.H > 240 {
				t.Error("long command pushes output outside the visible area", output)
			}
			if err := ui.RightClick(command); err != nil {
				t.Fatal(err)
			}
			if err := ui.ChooseMenuItem("复制命令"); err != nil || ui.Clipboard() != command {
				t.Error("abbreviated heading loses the original multiline command", err)
			}
		})
	}
}

func TestChildCommandOutputsAndBackgroundCommandStayCompact(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	key := v.Model.WorkKey("s", "agent-a")
	detail := v.Model.WorkDetails[key]
	detail.Items = []domain.WorkItem{{Item: domain.Item{ID: "a", Kind: "tool", Presentation: "command", Name: "first child command", Text: "old child output", Final: true}}, {Item: domain.Item{ID: "b", Kind: "tool", Presentation: "command", Name: "latest child command", Text: "latest child output", Final: true}}}
	v.Model.WorkDetails[key] = detail
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "child-command-output.png")
	if ui.HasText("old child output") || ui.HasText("latest child output") {
		t.Error("child command outputs do not start collapsed")
	}
	ui.Click("latest child command")
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("latest child output") {
		t.Fatal("child command cannot be inspected manually")
	}
	command := "python3 - <<'PY'\n" + strings.Repeat("print('keep the original command')\n", 40) + "PY"
	key = v.Model.WorkKey("s", "command-a")
	detail = v.Model.WorkDetails[key]
	detail.Work.Brief = command
	v.Model.WorkDetails[key] = detail
	ui.Click("查看后台任务：运行回归测试")
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "background-command.png")
	heading, ok := ui.Find(command)
	if !ok || heading.H <= 19 || heading.H > 36.5 {
		t.Error("background command does not use at most two lines", heading, ok)
	}
}

func TestCommandDisclosureDoesNotLeakAcrossSessions(t *testing.T) {
	a, b := New(layoutModel(), nil, ""), New(layoutModel(), nil, "")
	a.Model.SessionID, b.Model.SessionID = "session-a", "session-b"
	b.Model.Collapsed = a.Model.Collapsed
	left := []domain.Item{{ID: "same", Kind: "tool", Presentation: "command", Name: "A first", Text: "A older output"}, {ID: "next", Kind: "tool", Presentation: "command", Name: "A latest", Text: "A latest output"}}
	right := []domain.Item{{ID: "same", Kind: "tool", Presentation: "command", Name: "B latest", Text: "B latest output"}}
	ui := n.NewTester(func(c *n.Context) {
		n.Row(c).Fill().Children(func() {
			for i, v := range []*View{a, b} {
				items := left
				if i == 1 {
					items = right
				}
				n.Column(c.Key(i)).Width(380).Padding(12).Gap(8).Children(func() {
					v.transcriptRows(items)
					for _, item := range items {
						v.item(c.Key(item.ID), c.Theme(), item)
					}
				})
			}
		})
	}, 800, 300)
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("A latest")
	ui.Click("B latest")
	ui.Advance(time.Second)
	if ui.HasText("A older output") || !ui.HasText("A latest output") || !ui.HasText("B latest output") {
		t.Fatal("equal opaque command IDs in different sessions share disclosure state", ui.Texts())
	}
}

func TestChildLongCommandHeadingUsesTwoLines(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			ui.SetDark(dark)
			command := "/bin/zsh -lc '" + strings.Repeat("检查边界条件并保留原始命令 ", 40) + "'"
			key := v.Model.WorkKey("s", "agent-a")
			d := v.Model.WorkDetails[key]
			d.Items = []domain.WorkItem{{Item: domain.Item{ID: "long-child-command", Kind: "tool", Presentation: "command", Name: command, Text: "child result", Final: true}}}
			v.Model.WorkDetails[key] = d
			ui.Click("查看子 Agent：检查边界条件")
			ui.Advance(time.Second)
			heading, ok := ui.Find(command)
			if !ok || heading.H <= 21 || heading.H > 40.5 {
				t.Error("child command does not use two bounded lines", heading)
			}
			if err := ui.RightClick(command); err != nil {
				t.Fatal(err)
			}
			if err := ui.ChooseMenuItem("复制命令"); err != nil || ui.Clipboard() != command {
				t.Error("two-line child command loses original text", err)
			}
		})
	}
}

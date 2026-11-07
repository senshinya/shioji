package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/domain"
	"strings"
	"time"
)

type activityPatch struct {
	Source string
	State  editor.DiffState
}

var fileActivityIcon = svg("M5 2h7l4 4v12H5zM12 2v5h4M8 11h5M8 14h5")
var agentActivityIcon = svg("M3 4h6v5H3zM11 11h6v5h-6zM6 9v5h5")

func activityIcon(kind string) *n.SVG {
	switch kind {
	case "files", "context":
		return fileActivityIcon
	case "search", "review":
		return searchIcon
	case "agents":
		return agentActivityIcon
	case "reasoning":
		return questionIcon
	}
	return terminalIcon
}
func activityStatus(status string) string {
	switch status {
	case "running", "inProgress":
		return "进行中"
	case "failed":
		return "失败"
	case "declined":
		return "已拒绝"
	case "completed":
		return "已完成"
	case "interrupted":
		return "已中断"
	}
	return status
}

func (v *View) activityItem(c *n.Context, t *n.Theme, item domain.Item) {
	key := "activity:" + v.Model.SessionID + "/" + item.ID
	if _, seen := v.Model.Collapsed[key]; !seen {
		v.Model.Collapsed[key] = item.Kind == "activity"
	}
	hasBody := item.Text != "" || len(item.Parts) > 0 || len(item.Files) > 0 || len(item.Agents) > 0
	label := item.Name
	if label == "" {
		label = "工具活动"
	}
	button := n.ButtonBase(c.Key("activity-heading")).FillWidth().MinHeight(20).Justify(n.Start).Label(label)
	color := textHoverColor(c, button, t.TextMuted)
	button.Children(func() {
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
			if item.Kind == "activity" {
				inlineSymbol(c, activityIcon(item.Presentation), label, font, 14, color)
			}
			n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(color).Grow(1)
			if item.Status != "" {
				n.Text(c, activityStatus(item.Status)).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			if hasBody {
				angle := float32(0)
				if v.Model.Collapsed[key] {
					angle = -90
				}
				inlineSymbol(c, chevron, label, font, 10, color, angle)
			}
		})
	})
	if button.Clicked() && hasBody {
		if list := v.lists[v.Model.SessionID]; list != nil {
			list.KeepRow(v.currentTranscriptRow, button.Bounds().Y-list.Bounds(c).Y)
		}
		v.Model.Collapsed[key] = !v.Model.Collapsed[key]
	}
	if !hasBody || v.Model.Collapsed[key] {
		return
	}
	n.Column(c.Key("activity-body")).FillWidth().Gap(8).Transition(disclosureMotion(c, 160*time.Millisecond)).Children(func() {
		if item.Text != "" {
			text := n.Text(c, item.Text).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
			v.decoratePlainFind(c, text, item, nil)
		}
		for i, file := range item.Files {
			n.Column(c.Key(fmt.Sprint("file:", i))).FillWidth().Gap(6).Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
					n.Text(c, file.Path).FontSize(11).FixedLineHeight(20).Grow(1).Selectable()
					kind := map[string]string{"add": "新增", "delete": "删除", "update": "修改"}[file.Kind]
					if file.Destination != "" {
						kind = "移动"
					}
					n.Text(c, kind).FontSize(11).TextColor(t.TextMuted)
				})
				if file.Destination != "" {
					n.Text(c, "→ "+file.Destination).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				}
				if file.Patch != "" {
					v.activityDiff(c, t, key+fmt.Sprint(i), file)
				}
			})
		}
		for i, part := range item.Parts {
			n.Column(c.Key(fmt.Sprint("part:", i))).FillWidth().Gap(4).Children(func() {
				if part.Label != "" {
					n.Text(c, part.Label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
				}
				switch part.Kind {
				case "code":
					n.Scroll(c).FillWidth().MaxHeight(180).Children(func() {
						n.Text(c, part.Text).FillWidth().Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).Padding(8, 10).Radius(6).Background(codeSurface(t)).Selectable()
					})
				case "markdown":
					v.activityMarkdown.Theme = v.md.Theme
					v.activityMarkdown.ShareSelection = true
					v.activityMarkdown.WrapCode = true
					markdown.View(c, part.Text, &v.activityMarkdown).FillWidth().FontSize(12)
				default:
					n.Text(c, part.Text).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
				}
			})
		}
		for _, agent := range item.Agents {
			n.Column(c.Key(agent.ID)).FillWidth().Gap(4).Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
					font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
					statusDot(c, agent.ID, font, t.TextMuted, false)
					n.Text(c, agent.ID).FontSize(12).Grow(1)
					n.Text(c, activityStatus(agent.Status)).FontSize(11).TextColor(t.TextMuted)
				})
				if agent.Message != "" {
					n.Text(c, agent.Message).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingX(18)
				}
			})
		}
		if item.Truncated {
			n.Text(c, "内容过长，已截断显示").FontSize(11).TextColor(t.TextMuted)
		}
	})
}

func (v *View) activityDiff(c *n.Context, t *n.Theme, key string, file domain.FileChange) {
	if v.activityPatches == nil {
		v.activityPatches = map[string]*activityPatch{}
	}
	state := v.activityPatches[key]
	if state == nil || state.Source != file.Patch {
		state = &activityPatch{Source: file.Patch, State: editor.DiffState{Layout: editor.DiffUnified, Wrap: true}}
		patch := file.Patch
		if !strings.Contains(patch, "@@") {
			old, new := []string{}, []string{}
			for _, line := range strings.Split(patch, "\n") {
				if strings.HasPrefix(line, "-") {
					old = append(old, line[1:])
				}
				if strings.HasPrefix(line, "+") {
					new = append(new, line[1:])
				}
			}
			state.State.Old, state.State.New = editor.NewBuffer(strings.Join(old, "\n")), editor.NewBuffer(strings.Join(new, "\n"))
		} else if files, err := editor.ParsePatch(patch); err == nil && len(files) > 0 {
			state.State.SetPatch(files[0])
		} else {
			n.Text(c, patch).Font(v.Model.Preferences.CodeFont).FontSize(11).Selectable()
			return
		}
		v.activityPatches[key] = state
	}
	added, deleted, addFill, delFill := gitColors(t)
	state.State.Font = n.Font{Family: "Menlo", Size: 11, Weight: 400, Antialiased: true}
	state.State.LineHeight = 20.0 / 11
	state.State.Indicators = editor.DiffClassic
	state.State.Theme = &editor.Theme{Background: codeSurface(t), Text: t.Text, LineNumber: subtle(t), Added: added, Deleted: deleted, AddedBackground: addFill, DeletedBackground: delFill}
	editor.DiffView(c.Key(key), &state.State).FillWidth().Height(min(220, float32(strings.Count(file.Patch, "\n")+3)*20)).Label("文件补丁：" + file.Path)
}

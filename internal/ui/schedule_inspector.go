package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

var scheduleEditIcon = designIcon("m4 13 9-9 3 3-9 9-4 1 1-4Zm8-8 3 3")

const scheduleActionInset = 6
const schedulePlayGlyphInset = 2.5     // Left ink edge in the 14 DIP slot.
const scheduleTrailingIconOverhang = 5 // Match the history arrow's visible right edge.

func (v *View) runScheduleNow(g *schedulePageState, task domain.Schedule, source scheduleConnection, q protocol.ScheduleCommand) {
	if g.unconfirmedRuns == nil {
		g.unconfirmedRuns = make(map[scheduleKey]string)
	}
	key := scheduleIdentity(task)
	q.RunID = g.unconfirmedRuns[key]
	if q.RunID == "" {
		q.RunID = domain.NewID()
		g.unconfirmedRuns[key] = q.RunID
	}
	v.scheduleCommand(g, source, "schedule.run", q, func(_ json.RawMessage) { v.loadScheduleDetail(g, task, source) })
}

func (v *View) scheduleInspector(c *n.Context, t *n.Theme, g *schedulePageState, task domain.Schedule, inset float32, compact bool) {
	panel := n.Column(c.Key("schedule-inspector")).FillHeight().MinWidth(0)
	if compact {
		panel.FillWidth().Grow(1).AlignItems(n.Center)
	} else {
		panel.Width(scheduleInspectorWidth).Shrink(0).BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	}
	panel.Children(func() {
		n.Column(c).FillWidth().MaxWidth(912).Grow(1).MinHeight(0).Padding(0, inset, 24, inset).Children(func() {
			source := scheduleRoute(v.Model, task.DeviceID)
			workspace, exists := scheduleWorkspace(v.Model, task.WorkspaceID)
			change := exists && !workspace.Archived && source.online && !g.busy
			context := workspace.Name + " · " + v.Model.DeviceName(task.DeviceID)
			if compact {
				n.Row(c.Key("schedule-compact-detail-title")).FillWidth().Height(mainPageHeaderHeight).Padding(mainPageTopInset, 0, mainToolbarBottomInset, 0).
					Gap(8).AlignItems(n.Center).DragWindow().Children(func() {
					if iconButton(c, conversationBackIcon, "返回任务列表").MarginStart(-5).Clicked() {
						g.pick(scheduleKey{})
					}
					n.Text(c, task.Name).MinWidth(0).FontSize(20).FontWeight(500).FixedLineHeight(28).SingleLine().Ellipsis("…")
					n.Text(c, context).Grow(1).MinWidth(0).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…").TextColor(t.TextMuted)
				})
			} else {
				n.Row(c.Key("schedule-detail-title")).FillWidth().Height(mainPageHeaderHeight).Padding(mainPageTopInset, 0, 0, 0).
					Gap(4).AlignItems(n.Start).DragWindow().Children(func() {
					n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						n.Text(c, task.Name).FontSize(14).FontWeight(500).FixedLineHeight(20).MinWidth(0).
							SingleLine().Ellipsis("…").TooltipWhenTruncated(task.Name)
						n.Text(c, context).MinWidth(0).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…").TextColor(t.TextMuted)
					})
					if iconButton(c, closeIcon, "关闭任务详情").MarginEnd(-scheduleTrailingIconOverhang).Clicked() {
						g.pick(scheduleKey{})
					}
				})
			}
			n.Row(c.Key("schedule-actions")).FillWidth().Height(44).AlignItems(n.Center).Gap(6).Children(func() {
				q := protocol.ScheduleCommand{ID: task.ID, Revision: task.Revision}
				if scheduleAction(c, t, "立即运行", schedulePlayIcon).MarginStart(-scheduleActionInset - schedulePlayGlyphInset).Disabled(!change).Clicked() {
					v.runScheduleNow(g, task, source, q)
				}
				label, glyph := "暂停", schedulePauseIcon
				if !task.Enabled {
					label, glyph = "恢复", schedulePlayIcon
				}
				loaded := g.detail.Schedule.ID == task.ID && g.detail.Schedule.Revision == task.Revision
				if scheduleAction(c, t, label, glyph).Disabled(!change || !loaded).Clicked() {
					input := g.detail.Schedule
					input.Enabled = !task.Enabled
					v.saveSchedule(g, source, input, false)
				}
				n.Spacer(c).Grow(1)
				if iconButton(c, scheduleEditIcon, "编辑任务").Disabled(!change || !loaded).Clicked() {
					v.editSchedule(g, g.detail.Schedule)
				}
				if iconButton(c, archiveDeleteIcon, "删除任务").MarginEnd(-scheduleTrailingIconOverhang).Disabled(!source.online || g.busy).Clicked() {
					g.confirmDelete, g.deleteTarget = true, task
				}
			})
			if g.message != "" {
				n.Text(c, g.message).FontSize(12).TextColor(t.Danger)
			}
			n.Scroll(c.Key("schedule-detail-scroll")).FillWidth().Grow(1).MinHeight(0).TrackScroll(&g.history).Label("任务详情与运行历史").Children(func() {
				n.Column(c).FillWidth().Children(func() {
					n.Column(c).FillWidth().PaddingY(5).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
						scheduleInfo(c, t, "下一次", scheduleNext(task, source.online, workspace.Archived))
						rule := scheduleRuleLabel(task.Rule)
						if task.Rule.Kind != "interval" && task.Rule.Time != "" {
							rule += " · " + task.Rule.Time
						}
						scheduleInfo(c, t, "重复规则", rule)
						scheduleInfo(c, t, "运行配置", scheduleConfigLabel(task, v.scheduleReadiness(task)))
						scheduleInfo(c, t, "会话", "每次新开会话")
					})
					n.Text(c, "运行历史").FontWeight(500).FontSize(13).FixedLineHeight(20).Margin(12, 0, 4, 0)
					if len(g.detail.Runs) == 0 {
						label := "暂无运行记录"
						if g.detail.Schedule.ID == "" {
							label = "正在载入运行历史…"
							if !source.online {
								label = "设备离线，运行历史暂不可用"
							}
						}
						n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
					}
					for _, run := range g.detail.Runs {
						v.scheduleHistoryRow(c, t, g, task, run)
					}
				})
			})
		})
	})
}

func scheduleAction(c *n.Context, t *n.Theme, label string, icon *n.SVG) n.Element {
	return control(c).Height(28).PaddingX(scheduleActionInset).Radius(6).Label(label).Children(func() {
		inlineIconText(c, icon, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 5, t.TextMuted)
	})
}

func scheduleInfo(c *n.Context, t *n.Theme, label, value string) {
	n.Row(c.Key(label)).FillWidth().MinHeight(28).PaddingY(4).Gap(8).AlignItems(n.Center).Children(func() {
		n.Text(c, label).Width(64).Shrink(0).FontSize(12).TextColor(t.TextMuted)
		n.Text(c, value).Grow(1).MinWidth(0).FontSize(12).FixedLineHeight(18)
	})
}

func scheduleConfigLabel(task domain.Schedule, ready domain.Readiness) string {
	model := task.Config.Model
	for _, item := range ready.Models {
		if item.ID == model && item.Name != "" {
			model = item.Name
			break
		}
	}
	if model == "" {
		model = "默认模型"
	}
	return model + " · " + schedulePermission(task.Config.Permission, ready)
}

func (v *View) scheduleHistoryRow(c *n.Context, t *n.Theme, g *schedulePageState, task domain.Schedule, run domain.ScheduleRun) {
	stamp := scheduleDate(run.At, task.Zone, "01 月 02 日 15:04")
	if run.Manual {
		stamp += " · 手动运行"
	}
	meta := run.Error
	if !run.StartedAt.IsZero() && run.FinishedAt.After(run.StartedAt) && meta == "" {
		meta = fmt.Sprintf("持续 %s", run.FinishedAt.Sub(run.StartedAt).Round(time.Second))
	}
	switch run.SessionLink {
	case "archived":
		meta = joinScheduleMetadata("会话已归档", meta)
	case "deleted":
		meta = joinScheduleMetadata("会话已删除", meta)
	}
	draw := func() {
		n.Column(c).Grow(1).MinWidth(0).Children(func() {
			n.Text(c, stamp).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…")
			if meta != "" {
				n.Text(c, meta).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).SingleLine().Ellipsis("…").TooltipWhenTruncated(meta)
			}
		})
		n.Row(c).Width(68).Shrink(0).Children(func() { scheduleStatus(c, t, run.State) })
		if run.SessionLink == "open" || run.SessionLink == "archived" {
			n.Icon(c, disclosureRight).Size(12, 12).TextColor(t.TextMuted)
		} else {
			n.Spacer(c).Width(12).Grow(0).Shrink(0)
		}
	}
	if run.SessionLink == "open" || run.SessionLink == "archived" {
		row := control(c.Key("schedule-run:"+run.ID)).FillWidth().Height(44).Radius(0).Label("打开会话 · "+stamp).
			BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
			n.Row(c).FillWidth().Height(44).AlignItems(n.Center).Gap(6).Children(draw)
		})
		if row.Clicked() {
			if slices.ContainsFunc(v.Model.Snapshot.Sessions, func(s domain.Session) bool {
				return s.ID == run.SessionID && s.WorkspaceID == task.WorkspaceID
			}) {
				v.navigateSession(run.SessionID, task.WorkspaceID)
				g.session = run.SessionID
			} else {
				g.message, g.refreshAt = "会话暂不可用，请核对最新记录", time.Time{}
			}
		}
		return
	}
	n.Row(c.Key("schedule-run:"+run.ID)).FillWidth().Height(44).AlignItems(n.Center).Gap(6).
		BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(draw)
}

func joinScheduleMetadata(primary, extra string) string {
	if extra == "" {
		return primary
	}
	return primary + " · " + extra
}

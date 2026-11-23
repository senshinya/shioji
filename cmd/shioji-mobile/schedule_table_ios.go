package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

const (
	mobileScheduleSplitMin       = 760
	mobileScheduleInspectorWidth = 296
	mobileScheduleFourColumnMin  = 436
)

var (
	schedulePlaySymbol  = mobileSymbol(`<path d="m8 5 11 7-11 7z"/>`)
	schedulePauseSymbol = mobileSymbol(`<path d="M9 6v12m6-12v12"/>`)
	scheduleTrashSymbol = mobileSymbol(`<path d="M5 7h14M9 7V4h6v3m2 0-1 13H8L7 7m3 4v5m4-5v5"/>`)
	scheduleMinusSymbol = mobileSymbol(`<path d="M6 12h12"/>`)
)

type mobileScheduleTableRow struct {
	mobileclient.ScheduleRow
	device   string
	checking bool
}

func mobileScheduleKey(task domain.Schedule) [2]string {
	return [2]string{task.DeviceID, task.ID}
}

// The header and the body use identical column weights at both sizes.
func mobileScheduleColumns(c *ui.Context, withWorkspace bool, task, owner, next, result func(*ui.Context)) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).PassThrough().Children(func() {
		column := func(key string, weight float32, draw func(*ui.Context)) {
			cell := c.Key(key)
			ui.Column(cell).Basis(0).Grow(weight).MinWidth(0).Gap(4).PassThrough().Children(func() { draw(cell) })
		}
		column("task", 1.64, task)
		if withWorkspace {
			column("workspace", 1.15, owner)
		}
		column("next", 1.02, next)
		column("result", .88, result)
	})
}

func mobileScheduleTableHeader(c *ui.Context, withWorkspace bool) {
	p := palette(c)
	heading := func(title string, alignRight bool) func(*ui.Context) {
		return func(c *ui.Context) {
			text := oneLine(c, title, captionTextSize, p.secondary)
			if alignRight {
				text.AlignSelf(ui.End)
			}
		}
	}
	ui.Column(c).FillWidth().Margin(12, 0, 4).Padding(6, 6, 5).BorderWidth(0, 0, .5, 0).BorderColor(p.line).Children(func() {
		mobileScheduleColumns(c, withWorkspace, heading("任务", false), heading("工作区", false),
			heading("下一次", false), heading("最近一次", true))
	})
}

func drawMobileScheduleRow(c *ui.Context, row mobileScheduleTableRow, withWorkspace, selected, disabled bool, open func()) {
	task := row.Task
	next, cadence := mobileScheduleTiming(row)
	result, glyph, ink := mobileScheduleOutcome(c, task.Latest)
	p := palette(c)
	button := mobileButton(c, task.Name, open).FillWidth().MinHeight(max(56, mobileTapSize(c))).
		Padding(8, 6).Radius(6).AlignItems(ui.Center).Disabled(disabled).
		Value(strings.Join([]string{row.Workspace.Name, row.device, next, cadence, result}, "，"))
	if selected {
		button.Background(p.selected)
	}
	button.Children(func() {
		text := func(c *ui.Context, title, subtitle string) {
			oneLine(c, title, controlTextSize, p.ink).MinWidth(0)
			if subtitle != "" {
				oneLine(c, subtitle, captionTextSize, p.secondary).MinWidth(0)
			}
		}
		mobileScheduleColumns(c, withWorkspace, func(c *ui.Context) {
			ownership := ""
			if !withWorkspace {
				ownership = row.Workspace.Name + " · " + row.device
			}
			text(c, task.Name, ownership)
		}, func(c *ui.Context) {
			text(c, row.Workspace.Name, row.device)
		}, func(c *ui.Context) {
			text(c, next, cadence)
		}, func(c *ui.Context) {
			ui.Row(c).AlignSelf(ui.End).MinWidth(0).Gap(4).AlignItems(ui.Baseline).PassThrough().Children(func() {
				alignedSymbol(c, glyph, result, mobileStatusFont(c, captionTextSize), 11, ink)
				oneLine(c, result, captionTextSize, ink).MinWidth(0)
			})
		})
	})
}

func mobileScheduleTiming(row mobileScheduleTableRow) (next, cadence string) {
	task := row.Task
	cadence = mobileScheduleCadence(task.Rule)
	switch {
	case row.checking:
		return "正在核对", cadence
	case row.Status == mobileclient.ScheduleOffline:
		if !task.NextAt.IsZero() {
			cadence = "原定 " + mobileScheduleListDate(task.NextAt, task.Zone)
		}
		return "设备离线", cadence
	case row.Status == mobileclient.ScheduleArchived:
		return "工作区已归档", cadence
	case row.Status == mobileclient.SchedulePaused:
		return "已暂停", cadence
	case task.NextAt.IsZero():
		return "无下一次", cadence
	default:
		return mobileScheduleListDate(task.NextAt, task.Zone), cadence
	}
}

func mobileScheduleListDate(at time.Time, zone string) string {
	if location := mobileScheduleLocation(zone); location != nil {
		return at.In(location).Format("01/02 15:04")
	}
	return at.UTC().Format("01/02 15:04") + " UTC"
}

func mobileScheduleCadence(rule domain.ScheduleRule) string {
	switch rule.Kind {
	case "once":
		return "一次"
	case "daily":
		return "每天"
	case "weekly":
		if slices.Equal(rule.Days, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}) {
			return "每个工作日"
		}
		days := []string{"日", "一", "二", "三", "四", "五", "六"}
		result := "每周"
		for _, day := range rule.Days {
			if day >= time.Sunday && day <= time.Saturday {
				result += days[day]
			}
		}
		return result
	case "interval":
		if rule.Minutes >= 60 && rule.Minutes%60 == 0 {
			return fmt.Sprintf("每 %d 小时", rule.Minutes/60)
		}
		return fmt.Sprintf("每 %d 分钟", rule.Minutes)
	}
	return "—"
}

func mobileScheduleOutcome(c *ui.Context, run *domain.ScheduleRun) (string, *ui.SVG, ui.Color) {
	p := palette(c)
	if run == nil {
		return "未运行", scheduleMinusSymbol, p.secondary
	}
	switch run.State {
	case domain.ScheduleCompleted:
		return "已完成", checkSymbol, p.secondary
	case domain.ScheduleApproval:
		return "待审批", infoSymbol, p.warning
	case domain.ScheduleFailed:
		return "已失败", infoSymbol, p.diffRemoved
	case domain.ScheduleMissed:
		return "已错过", scheduleMinusSymbol, p.secondary
	default:
		if label := scheduleRunLabel(run.State); label != "" {
			return label, clockSymbol, p.secondary
		}
		return "待核对", infoSymbol, p.secondary
	}
}

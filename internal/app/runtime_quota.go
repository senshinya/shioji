package app

import (
	"fmt"
	"shioji/internal/domain"
)

func RuntimeQuotaLabels(window domain.RuntimeQuotaWindow) (title, value string) {
	period := "额度"
	switch window.DurationSeconds {
	case 18000:
		period = "5 小时额度"
	case 604800:
		period = "每周额度"
	}
	title = window.Group + " · " + period
	value = fmt.Sprintf("已用 %.0f%%", window.UsedPercent)
	if window.ResetsAt != nil {
		value += " · " + window.ResetsAt.Local().Format("01-02 15:04") + " 重置"
	}
	return title, value
}

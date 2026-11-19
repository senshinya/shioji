package ui

import (
	"shioji/internal/app"
	"time"

	n "github.com/egoist/mygo/ui"
)

var workerUpdateIcon = svg("M10 13V2M6 6l4-4 4 4M3 12v5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-5")

// Use the existing device-details hit area, insets and row rhythm for both actions.
func devicePopoverAction(c *n.Context) n.Element {
	return control(c).AlignSelf(n.Stretch).Justify(n.Start).Height(26).MarginY(1).MarginX(-6).PaddingX(6).Radius(4)
}

type deviceWorkerUpdateContent struct {
	label, help  string
	busy, failed bool
}

func deviceWorkerUpdateCaption(state *app.DeviceState) (string, bool) {
	service := state.WorkerService
	if service == nil {
		return "", false
	}
	switch service.Phase {
	case app.WorkerAvailable:
		if state.Connected {
			return "更新执行服务", false
		}
	case app.WorkerWaiting:
		return "等待空闲后更新…", false
	case app.WorkerUpdating:
		if label := service.UploadLabel(); label != "" {
			return label + "…", true
		}
		switch service.OperationPhase {
		case "preparing":
			return "正在准备更新…", true
		case "migrating":
			return "正在迁移数据库…", true
		case "applied":
			return "正在重新连接…", true
		case "", "observing":
			return "正在核对更新结果…", true
		default:
			return "正在更新执行服务…", true
		}
	case app.WorkerUpdateFailed:
		return "更新失败，重试", false
	case app.WorkerLegacy:
		if state.Connected && service.Available != nil && (service.Build == nil || service.Build.BuildID != service.Available.BuildID) {
			return "更新执行服务…", false
		}
	}
	return "", false
}

func (v *View) deviceWorkerUpdateAction(c *n.Context, t *n.Theme, key string, open *bool) {
	state := v.deviceState(key)
	service := state.WorkerService
	label, busy := deviceWorkerUpdateCaption(state)
	visible := label != ""
	// The row owns the real height. Its siblings and the anchored popup follow
	// directly; do not also ease the popup's size. Padding retains hover insets.
	frame := n.Column(c).AlignSelf(n.Stretch).MarginX(-6).Clip()
	content := n.Local(frame, "update-content", func() deviceWorkerUpdateContent { return deviceWorkerUpdateContent{} })
	if visible {
		help := service.Message
		if help != "" {
			help += "\n"
		}
		help += "当前构建：" + app.WorkerBuildLabel(service.Build)
		if service.Available != nil {
			help += "\n可用构建：" + app.WorkerBuildLabel(service.Available)
		}
		*content = deviceWorkerUpdateContent{label: label, help: help, busy: busy, failed: service.Phase == app.WorkerUpdateFailed}
	}
	disabled := v.Controller == nil || busy || !visible
	if service != nil && service.Phase == app.WorkerWaiting {
		disabled = disabled || !state.Connected || service.OperationID == "" || service.Available == nil
	}
	if service != nil && !state.Connected && service.OperationID == "" {
		disabled = true
	}
	animatedContentHeight(c, frame, visible, 180*time.Millisecond, func() float32 {
		label := content.label
		n.Column(c).FillWidth().PaddingX(6).Children(func() {
			action := devicePopoverAction(c).Label("浮层执行服务更新").Tooltip(content.help).Disabled(disabled).Children(func() {
				color := t.TextMuted
				if content.failed {
					color = t.Danger
				}
				font := n.Font{Family: t.Font, Size: 12, Weight: 400}
				if content.busy {
					n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
						serviceSpinner(c.Key("progress"), label, font, color).Label("浮层更新图标")
						n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(color).Label("浮层更新文字")
					})
				} else {
					inlineIconText(c, workerUpdateIcon, label, font, 14, 6, color)
				}
			})
			if !visible || !action.Clicked() {
				return
			}
			switch service.Phase {
			case app.WorkerWaiting:
				*open = false
				if service.RequiresSchemaMigration() {
					v.deviceSelected, v.deviceList, v.devices = key, false, true
					return
				}
				v.beginWorkerUpdateConfirmation(key, state, true)
			case app.WorkerLegacy:
				*open = false
				v.deviceSelected, v.deviceList, v.devices = key, false, true
			default:
				if service.RequiresSchemaMigration() {
					*open = false
				}
				v.requestWorkerUpdate(key, state)
			}
		})
		return 0
	})
}

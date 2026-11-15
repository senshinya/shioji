package ui

import (
	"context"
	"errors"
	"fmt"
	"shioji/internal/app"
	"shioji/internal/domain"
	"time"

	n "github.com/egoist/mygo/ui"
)

type storagePhase uint8

const (
	storageUnloaded storagePhase = iota
	storageLoading
	storageReady
	storageCleaning
	storageCleaned
	storagePartial
	storageFailed
)

type deviceStorageState struct {
	phase        storagePhase
	epoch        string
	request      uint64
	report       *domain.StorageReport
	error        string
	failedClean  bool
	failuresOpen bool
}

func (s *deviceStorageState) busy() bool {
	return s.phase == storageLoading || s.phase == storageCleaning
}
func (s *deviceStorageState) finish(report domain.StorageReport, err error, clean bool) {
	if err != nil {
		s.phase = storageFailed
		s.error = err.Error()
		s.failedClean = clean
		return
	}
	s.report = &report
	s.error = ""
	s.failedClean = false
	s.failuresOpen = false
	s.phase = storageReady
	if clean {
		s.phase = storageCleaned
		if len(report.Failures) > 0 {
			s.phase = storagePartial
		}
	}
}

func (v *View) deviceStorage(c *n.Context, t *n.Theme, key string, device *app.DeviceState) {
	if v.deviceStorageStates == nil {
		v.deviceStorageStates = map[string]*deviceStorageState{}
	}
	s := v.deviceStorageStates[key]
	if s == nil || s.epoch != device.ConnectionEpoch {
		s = &deviceStorageState{epoch: device.ConnectionEpoch}
		v.deviceStorageStates[key] = s
	}
	reason := ""
	if !device.Connected {
		reason = "设备未连接"
	} else if device.WorkerMinor < 31 {
		reason = "更新执行服务后可管理存储"
	}
	available := reason == "" && v.Controller != nil
	if s.phase == storageUnloaded && available {
		v.readDeviceStorage(key, s, false)
	}
	v.storageGroup(c, t, "存储", s, available, reason, func(clean bool) { v.readDeviceStorage(key, s, clean) })
	if v.clientStorageState == nil {
		v.clientStorageState = &deviceStorageState{}
	}
	local := v.clientStorageState
	clientAvailable := v.Controller != nil && v.StatePath != ""
	if local.phase == storageUnloaded && clientAvailable {
		v.readClientStorage(local, false)
	}
	v.storageGroup(c, t, "本机显示缓存", local, clientAvailable, "", func(clean bool) { v.readClientStorage(local, clean) })
}

func (v *View) readDeviceStorage(key string, s *deviceStorageState, clean bool) {
	if v.Controller == nil || s.busy() {
		return
	}
	target, err := v.Model.StorageTarget(v.Controller.Directory, key)
	if err != nil {
		s.finish(domain.StorageReport{}, err, clean)
		return
	}
	s.phase = storageLoading
	if clean {
		s.phase = storageCleaning
	}
	s.error = ""
	s.request++
	request := s.request
	epoch := s.epoch
	controller := v.Controller
	controller.AttachmentTask(func() app.Update {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var report domain.StorageReport
		var err error
		if target.SSH != nil {
			routed, routeErr := controller.TailnetSSH(*target.SSH)
			target.SSH, err = &routed, routeErr
		}
		if err == nil {
			report, err = target.Read(ctx, clean)
		}
		return func(m *app.Model) {
			v.applyStorageResult(m, key, s, epoch, request, report, err, clean)
		}
	})
}

func (v *View) applyStorageResult(m *app.Model, key string, s *deviceStorageState, epoch string, request uint64, report domain.StorageReport, err error, clean bool) {
	device := m.DeviceStates[key]
	if v.deviceStorageStates[key] != s || s.epoch != epoch || s.request != request || device == nil || !device.Connected || device.ConnectionEpoch != epoch {
		return
	}
	s.finish(report, err, clean)
}

func (v *View) readClientStorage(s *deviceStorageState, clean bool) {
	if v.Controller == nil || v.StatePath == "" || s.busy() {
		return
	}
	s.phase = storageLoading
	if clean {
		s.phase = storageCleaning
	}
	s.error = ""
	s.request++
	request := s.request
	cache := v.displayCache()
	linkedDirectory := v.linkedFileDirectory()
	v.Controller.AttachmentTask(func() app.Update {
		report, err := cache.Storage(clean)
		linked, linkedErr := app.LinkedFileStorage(linkedDirectory, clean)
		report.Usage.PreviewBytes += linked.Usage.PreviewBytes
		report.FreedBytes += linked.FreedBytes
		report.Failures = append(report.Failures, linked.Failures...)
		err = errors.Join(err, linkedErr)
		return func(*app.Model) {
			if s.request != request {
				return
			}
			s.finish(report, err, clean)
		}
	})
}

func storageCapacity(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	}
	if bytes < 1<<30 {
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(bytes)/(1<<30))
}

func (v *View) storageGroup(c *n.Context, t *n.Theme, title string, s *deviceStorageState, available bool, reason string, run func(bool)) {
	client := title == "本机显示缓存"
	n.Column(c.Key(title)).FillWidth().Gap(0).Label(title + "区域").Children(func() {
		deviceHeading(c, title, func() {
			n.Row(c).Gap(4).AlignItems(n.Center).Children(func() {
				if iconButton(c, refreshIcon, "刷新"+title+"统计").Disabled(!available || s.busy()).Clicked() {
					run(false)
				}
				if !client {
					value := "—"
					if s.report != nil {
						value = "共 " + storageCapacity(s.report.Usage.Total())
					}
					storageValue(c, t, value)
				}
			})
		})
		if client {
			v.storageUsageRow(c, t, "预览与缩略图", s, func(u domain.StorageUsage) int64 { return u.PreviewBytes })
		} else {
			v.storageUsageRow(c, t, "会话与附件", s, func(u domain.StorageUsage) int64 { return u.RetainedBytes })
			v.storageUsageRow(c, t, "可重建预览", s, func(u domain.StorageUsage) int64 { return u.PreviewBytes })
			v.storageUsageRow(c, t, "过期未完成上传", s, func(u domain.StorageUsage) int64 { return u.UploadBytes })
			v.storageUsageRow(c, t, "历史日志", s, func(u domain.StorageUsage) int64 { return u.LogBytes })
		}
		row := n.Row(c.Key("cleanup-line")).FillWidth().Height(configurationRowHeight).Gap(12).AlignItems(n.Center)
		row.Children(func() {
			value := "可回收 —"
			if s.report != nil {
				value = "可回收 " + storageCapacity(s.report.Usage.Reclaimable())
			}
			if client {
				value = "这台 Mac"
			}
			storageValue(c, t, value).Grow(1)
			caption := "暂不可用"
			if available {
				caption = "无需清理"
				if s.report != nil && s.report.Usage.Reclaimable() > 0 {
					caption = "清理可回收内容"
					if client {
						caption = "清理缓存"
					}
				}
			}
			switch s.phase {
			case storageLoading:
				caption = "正在统计…"
			case storageCleaning:
				caption = "清理中…"
			case storageCleaned, storagePartial:
				caption = "已释放 " + storageCapacity(s.report.FreedBytes)
			case storageFailed:
				caption = "统计失败 · 重试"
				if s.failedClean {
					caption = "清理失败 · 重试"
				}
			}
			canClean := s.report != nil && s.report.Usage.Reclaimable() > 0 && (s.phase == storageReady || s.phase == storageFailed)
			canRetry := s.phase == storageFailed
			if !available {
				caption = reason
				if reason == "更新执行服务后可管理存储" {
					caption = "需更新执行服务"
				}
				if caption == "" {
					caption = "暂不可用"
				}
			}
			label := "清理设备可回收内容"
			if client {
				label = "清理本机显示缓存"
			}
			// Keep the status outside the disabled hit target so a completed
			// result remains readable and shares the running caption's baseline.
			var button n.Element
			n.Row(c.Key("cleanup-action")).Height(28).MinWidth(148).PaddingX(8).MarginEnd(-8).AlignItems(n.Center).Justify(n.End).Children(func() {
				button = control(c.Key("hit"), 5).Absolute().Left(0).Right(0).Top(0).Bottom(0).Disabled(!available || s.busy() || (!canClean && !canRetry)).Label(label).Description(caption).Tooltip(reason)
				n.Row(c).Height(20).Gap(0).AlignItems(n.Baseline).Justify(n.End).Label(title + "清理状态").Children(func() {
					font := n.Font{Family: t.Font, Size: 12, Weight: 400}
					extent := float32(0)
					if s.busy() {
						extent = 1
					}
					slot := serviceSpinner(c.Key("spinner"), caption, font, t.TextMuted).Height(20).Clip()
					if !c.Preferences().ReduceMotion {
						extent = slot.AnimateTo("extent", extent, motionAnimation(c, 180*time.Millisecond))
					}
					slot.Width(20 * extent).Opacity(extent)
					text := n.Text(c.Key("caption"), caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					if !c.Preferences().ReduceMotion {
						text.ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
					}
				})
			})
			if button.Clicked() {
				run(s.phase != storageFailed || s.failedClean)
			}
		})
		v.storageFailures(c, t, s, available, run)
	})
}

func storageValue(c *n.Context, t *n.Theme, value string) n.Element {
	e := n.Text(c, value).FontSize(12).FixedLineHeight(20).FontFeatures("tnum").TextColor(t.TextMuted)
	if !c.Preferences().ReduceMotion {
		e.ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 260*time.Millisecond)})
	}
	return e
}

func (v *View) storageUsageRow(c *n.Context, t *n.Theme, label string, s *deviceStorageState, read func(domain.StorageUsage) int64) {
	n.Row(c.Key(label)).FillWidth().Height(configurationRowHeight).Gap(20).AlignItems(n.Center).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
		n.Text(c, label).FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).Grow(1)
		value := "—"
		if s.report != nil {
			value = storageCapacity(read(s.report.Usage))
		}
		e := storageValue(c, t, value)
		if label == "会话与附件" {
			e.Tooltip("保留原件，不参与清理")
		}
	})
}

func (v *View) storageFailures(c *n.Context, t *n.Theme, s *deviceStorageState, available bool, run func(bool)) {
	var failures []domain.StorageFailure
	if s.report != nil {
		failures = s.report.Failures
	}
	if s.error != "" {
		failures = []domain.StorageFailure{{Reason: s.error}}
	}
	frame := n.Column(c.Key("failure-area")).FillWidth().Clip()
	retained := n.Local(frame, "retained", func() []domain.StorageFailure { return failures })
	if len(failures) > 0 {
		*retained = failures
	}
	animatedContentHeight(c, frame, len(failures) > 0, 260*time.Millisecond, func() float32 {
		n.Row(c.Key("failure-header")).FillWidth().Height(32).AlignItems(n.Center).Children(func() {
			caption := fmt.Sprintf("%d 项未清理", len(*retained))
			if s.error != "" {
				caption = "查看原因"
			}
			button := control(c.Key("failures-toggle"), 5).Height(28).PaddingX(0).Label("展开存储清理失败详情").Children(func() {
				n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
					angle := float32(-90)
					if s.failuresOpen {
						angle = 0
					}
					inlineSymbol(c, chevron, caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted, angle)
					n.Text(c, caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				})
			})
			if button.Clicked() {
				s.failuresOpen = !s.failuresOpen
			}
			n.Spacer(c).Grow(1)
			if configurationAction(c, "重试").MarginEnd(-8).Disabled(!available || s.busy()).Clicked() {
				run(s.error == "" || s.failedClean)
			}
		})
		if s.failuresOpen {
			n.Column(c.Key("failure-details")).FillWidth().Padding(4, 12, 8).Radius(6).Background(t.Surface).Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
				for _, failure := range *retained {
					n.Row(c).FillWidth().MinHeight(32).Gap(12).AlignItems(n.Baseline).Children(func() {
						if failure.Name != "" {
							n.Text(c, failure.Name).SingleLine().MinWidth(0).Grow(1).FontSize(11).TooltipWhenTruncated(failure.Name)
						}
						n.Text(c, failure.Reason).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).MinWidth(0)
					})
				}
			})
		}
		return 0
	})
}

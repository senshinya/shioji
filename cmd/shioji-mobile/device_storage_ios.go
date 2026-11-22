package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type mobileDeviceStorage struct {
	device, epoch string
	remote        *mobileStorageReport
	local         mobileStorageReport
	localStarted  bool
}

type mobileStorageReport struct {
	report         *domain.StorageReport
	err            error
	busy, cleaning bool
	confirming     bool
	failuresOpen   bool
}

func (a *mobileApp) deviceStorage() *mobileDeviceStorage {
	s := &mobileDeviceStorage{remote: &mobileStorageReport{}}
	if a.preview {
		s.remote.report = &domain.StorageReport{Usage: domain.StorageUsage{
			RetainedBytes: 1610 << 20, UploadBytes: 280 << 20, LogBytes: 32 << 20,
		}}
		s.local.report = &domain.StorageReport{Usage: domain.StorageUsage{PreviewBytes: 38 << 20}}
	}
	return s
}

func (a *mobileApp) deviceStorageView(c *ui.Context, w *mygo.Window, s *mobileDeviceStorage) {
	device, epoch, owner := a.model.Snapshot.DeviceID, a.model.ConnectionEpoch, a.controller
	reason := ""
	if !a.preview {
		if !a.model.Connected || owner == nil || device == "" {
			reason = "设备未连接"
		} else if a.model.WorkerMinor < 31 {
			reason = "更新执行服务后可管理存储"
		}
	}
	valid := func() bool {
		return a.pageCurrent(w) && (a.preview || a.model.Connected &&
			a.model.Snapshot.DeviceID == device && a.model.ConnectionEpoch == epoch && a.controller == owner)
	}
	remote := func(clean, refresh bool) {
		if reason != "" || !valid() || s.remote.busy {
			return
		}
		if a.preview {
			s.remote.preview(clean)
			w.Invalidate()
			return
		}
		target := app.RemoteStorageTarget(device, owner.DialDeviceStream)
		s.remote.run(w, clean, valid, func() (domain.StorageReport, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			return target.Read(ctx, clean, refresh)
		})
	}
	if reason == "" && !a.preview && (s.device != device || s.epoch != epoch) {
		s.device, s.epoch = device, epoch
		s.remote = &mobileStorageReport{}
		remote(false, false)
	}
	local := func(clean bool) {
		if !a.pageCurrent(w) || s.local.busy {
			return
		}
		if a.preview {
			s.local.preview(clean)
			w.Invalidate()
			return
		}
		targets := a.displayStorageTargets()
		s.local.run(w, clean, func() bool { return a.pageCurrent(w) }, func() (domain.StorageReport, error) {
			return readMobileDisplayStorage(targets, clean)
		})
	}
	if !a.preview && !s.localStarted {
		s.localStarted = true
		local(false)
	}

	ui.Column(c.Key("remote-storage")).Children(func() {
		s.remote.heading(c, "存储", reason == "", true, func() { remote(false, true) })
		s.remote.usageRow(c, "会话与附件", func(u domain.StorageUsage) int64 { return u.RetainedBytes })
		s.remote.usageRow(c, "可重建预览", func(u domain.StorageUsage) int64 { return u.PreviewBytes })
		s.remote.usageRow(c, "过期未完成上传", func(u domain.StorageUsage) int64 { return u.UploadBytes })
		s.remote.usageRow(c, "历史日志", func(u domain.StorageUsage) int64 { return u.LogBytes })
		s.remote.cleanupRow(c, "清理缓存", reason, func(clean bool) {
			if !clean {
				remote(false, true)
				return
			}
			if !valid() || s.remote.confirming {
				return
			}
			s.remote.confirming = true
			ctx, cancel := context.WithCancel(context.Background())
			unregister := w.OnClosed(cancel)
			args := map[string]any{"window": w.NativeHandle(), "title": "清理设备缓存？",
				"detail": "将清理可重建预览、过期未完成上传和历史日志。"}
			go func() {
				defer cancel()
				var confirmed bool
				err := uikit.Call(ctx, "confirmStorageCleanup", args, &confirmed)
				mygo.RunOnMain(func() {
					unregister()
					if !valid() {
						return
					}
					s.remote.confirming = false
					if err != nil {
						s.remote.err, s.remote.cleaning = err, true
						w.Invalidate()
					} else if confirmed {
						remote(true, false)
					}
				})
			}()
		})
	})
	ui.Column(c.Key("local-storage")).Children(func() {
		s.local.heading(c, "本机显示缓存", true, false, func() { local(false) })
		s.local.usageRow(c, "预览与缩略图", func(u domain.StorageUsage) int64 { return u.PreviewBytes })
		s.local.cleanupRow(c, "清理本机显示缓存", "", local)
	})
}

func (s *mobileStorageReport) run(w *mygo.Window, clean bool, current func() bool, read func() (domain.StorageReport, error)) {
	s.err, s.busy, s.cleaning, s.failuresOpen = nil, true, clean, false
	w.Invalidate()
	go func() {
		report, err := read()
		mygo.RunOnMain(func() {
			s.busy = false
			if !current() {
				return
			}
			s.err = err
			if err == nil {
				s.report = &report
			}
			w.Invalidate()
		})
	}()
}

func (s *mobileStorageReport) preview(clean bool) {
	s.cleaning = clean
	s.failuresOpen = false
	if clean && s.report != nil {
		s.report = &domain.StorageReport{
			Usage:      domain.StorageUsage{RetainedBytes: s.report.Usage.RetainedBytes},
			FreedBytes: s.report.Usage.Reclaimable(),
		}
	}
}

func (s *mobileStorageReport) heading(c *ui.Context, title string, available, showTotal bool, refresh func()) {
	ui.Spacer(c.Key("heading-top")).Height(17).Shrink(0)
	ui.Row(c.Key("heading")).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(8).Children(func() {
		label(c, title, controlTextSize, palette(c).ink).FontWeight(620).Grow(1).MinWidth(0)
		if showTotal && s.report != nil {
			oneLine(c, "共 "+mobileStorageCapacity(s.report.Usage.Total()), captionTextSize, palette(c).secondary)
		}
		iconButton(c, "刷新"+title+"统计", refreshSymbol, 16, refresh).Disabled(!available || s.busy)
	})
}

func (s *mobileStorageReport) usageRow(c *ui.Context, title string, measure func(domain.StorageUsage) int64) {
	capacity := "—"
	if s.report != nil {
		capacity = mobileStorageCapacity(measure(s.report.Usage))
	}
	deviceLoadRow(c.Key(title), title, "", capacity, nil)
}

func (s *mobileStorageReport) cleanupRow(c *ui.Context, title, unavailable string, run func(bool)) {
	capacity := "—"
	if s.report != nil {
		capacity = mobileStorageCapacity(s.report.Usage.Reclaimable())
	}
	caption := "无需清理"
	canClean, retry := s.report != nil && s.report.Usage.Reclaimable() > 0, s.err != nil
	switch {
	case unavailable != "":
		caption = unavailable
	case s.busy && s.cleaning:
		caption = "清理中…"
	case s.busy:
		caption = "正在统计…"
	case retry && s.cleaning:
		caption = "清理失败 · 重试"
	case retry:
		caption = "统计失败 · 重试"
	case s.cleaning && s.report != nil:
		caption = "已释放 " + mobileStorageCapacity(s.report.FreedBytes)
	case canClean:
		caption = title
	}
	ui.Row(c.Key("cleanup")).MinHeight(max(formRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Row(c.Key("reclaimable")).Grow(1).Gap(6).AlignItems(ui.Baseline).MinWidth(0).Wrap().Children(func() {
			label(c, "可回收", controlTextSize, palette(c).ink)
			oneLine(c, capacity, captionTextSize, palette(c).secondary)
		})
		if unavailable == "" && !s.busy && (retry || canClean && !s.cleaning) {
			action(c.Key("action"), caption, func() { run(!retry || s.cleaning) }).PaddingX(0)
		} else {
			label(c.Key("result"), caption, controlTextSize, palette(c).secondary).Role(ui.RoleStatus)
		}
	})

	var failures []domain.StorageFailure
	if s.err != nil {
		failures = []domain.StorageFailure{{Reason: s.err.Error()}}
	} else if s.report != nil {
		failures = s.report.Failures
	}
	if len(failures) == 0 {
		return
	}
	ui.Row(c.Key("failures")).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(12).Children(func() {
		title := "查看原因"
		if s.err == nil {
			title = fmt.Sprintf("%d 项未清理", len(failures))
		}
		action(c, title, func() { s.failuresOpen = !s.failuresOpen }).PaddingX(0).Grow(1).Justify(ui.Start)
		if s.err == nil {
			action(c.Key("retry"), "重试", func() { run(true) }).Disabled(unavailable != "" || s.busy)
		}
	})
	mobileDisclosureBody(c.Key("reasons"), s.failuresOpen, func(body *ui.Context) {
		for i, failure := range failures {
			reason := failure.Reason
			if failure.Name != "" {
				reason = failure.Name + " · " + reason
			}
			mobileMessage(body.Key(fmt.Sprint(i)), reason)
		}
	})
}

func mobileStorageCapacity(count int64) string {
	switch {
	case count < 1<<10:
		return fmt.Sprintf("%d B", count)
	case count < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(count)/(1<<10))
	case count < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(count)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(count)/(1<<30))
	}
}

type mobileDisplayStorage struct {
	attachments *app.AttachmentCache
	linkedFiles string
}

func (a *mobileApp) displayStorageTargets() []mobileDisplayStorage {
	root := a.splitRoot()
	directories := map[string]bool{}
	targets := make([]mobileDisplayStorage, 0, len(root.savedDevices.Devices)+1)
	add := func(directory string) {
		if directory == "" || directories[directory] {
			return
		}
		directories[directory] = true
		targets = append(targets, mobileDisplayStorage{
			attachments: a.displayAttachmentCache(directory),
			linkedFiles: filepath.Join(directory, "cache", "linked-files"),
		})
	}
	add(a.directory)
	add(root.rootDirectory)
	for _, device := range root.savedDevices.Devices {
		if directory, err := mobileDeviceDirectory(root.rootDirectory, device.Slot); err == nil {
			add(directory)
		}
	}
	return targets
}

func readMobileDisplayStorage(targets []mobileDisplayStorage, clean bool) (domain.StorageReport, error) {
	var report domain.StorageReport
	var errs error
	merge := func(part domain.StorageReport, err error) {
		report.Usage.PreviewBytes += part.Usage.PreviewBytes
		report.FreedBytes += part.FreedBytes
		report.Failures = append(report.Failures, part.Failures...)
		errs = errors.Join(errs, err)
	}
	for _, target := range targets {
		merge(target.attachments.Storage(clean))
		merge(app.LinkedFileStorage(target.linkedFiles, clean))
	}
	return report, errs
}

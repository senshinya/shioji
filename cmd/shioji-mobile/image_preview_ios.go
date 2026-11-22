package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

const mobileImagePreviewType = "shiojiImagePreview"

type mobileImageReader struct {
	view *mygo.PluginView
}

func (r *mobileImageReader) open(window *mygo.Window, path, name string, reduceMotion, showsFilename bool) {
	if r.view != nil {
		return
	}
	r.view, _ = mygo.NewPluginView(window, mobileImagePreviewType, map[string]any{
		"path": path, "name": name, "reduceMotion": reduceMotion, "showsFilename": showsFilename,
	})
}

func (r *mobileImageReader) close() {
	if r.view != nil {
		r.view.Destroy()
		r.view = nil
	}
}

func (r *mobileImageReader) render(c *ui.Context) {
	if r.view == nil {
		ui.Column(c).Fill().Center().Children(func() { mobileMessage(c, "无法显示图片，可使用原生预览。") })
		return
	}
	// PluginView uses MyGo's embedded platform-view slot. The content of
	// this slot is a UIKit scroll view, so zoom and pan stay on this image.
	ui.WebView(c, r.view).Fill().MinWidth(0).MinHeight(0)
}

func (a *mobileApp) imageAttachmentPage(parent *mygo.Window, session domain.Session, item domain.Attachment) {
	if !a.pageCurrent(parent) {
		return
	}
	var reader mobileImageReader
	var request mobileRequest
	var w *mygo.Window
	var localPath string
	var load func()
	var cancel context.CancelFunc
	w = a.page("附件预览", func(c *ui.Context) {
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinHeight(0).Padding(pageTopInset, pageInset, 0).Children(func() {
				switch {
				case reader.view != nil:
					reader.render(c)
				default:
					oneLine(c, item.Name, bodyTextSize, palette(c).ink)
					if localPath != "" {
						reader.render(c)
					} else if request.busy {
						mobileMessage(c, "正在读取图片…")
					} else {
						request.status(c)
					}
				}
			})
			mobilePageFooter(c, func() {
				if localPath != "" {
					ui.Row(c).Gap(8).Children(func() {
						secondaryAction(c, "另存为…", func() {
							go func() { _, _ = mygo.Dialog.SaveCopy(localPath, item.Name) }()
						}).Grow(1).Basis(0)
						primaryAction(c, "原生预览", func() {
							a.openNativeFilePreview(w, localPath, item.Name, item.MIME)
						}).Grow(1).Basis(0)
					})
				} else if request.err != "" && !request.busy {
					primaryAction(c, "重试读取", load)
				}
			})
		})
	})
	w.OnClosed(func() {
		if cancel != nil {
			cancel()
		}
		reader.close()
	})
	load = func() {
		if request.busy {
			return
		}
		if a.directory == "" {
			request.err = "图片暂不可用"
			w.Invalidate()
			return
		}
		owner, device, epoch := a.controller, a.model.Snapshot.DeviceID, a.model.ConnectionEpoch
		connected := a.model.Connected && owner != nil
		staging := attachments.New(filepath.Join(a.directory, "attachments"))
		ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
		cancel = stop
		request.busy, request.err = true, ""
		w.Invalidate()
		go func() {
			defer stop()
			path, err := staging.Path(attachments.StagingWorkspace, item)
			fromWorker := err != nil && connected
			if fromWorker {
				worker := app.RemoteAttachmentTarget(device, session.WorkspaceID, owner.DialDeviceStream)
				path, err = worker.Download(ctx, staging, item)
			} else if err != nil {
				err = errors.New("连接执行设备后读取图片")
			}
			mygo.RunOnMain(func() {
				if !a.pageCurrent(w) {
					return
				}
				cancel = nil
				request.busy = false
				switch {
				case fromWorker && (owner != a.controller || epoch != a.model.ConnectionEpoch):
					request.err = "连接已改变，请重试"
				case err != nil:
					request.err = err.Error()
				default:
					localPath = path
					reader.open(w, path, item.Name, a.model.Preferences.ReduceMotion, true)
				}
				w.Invalidate()
			})
		}()
	}
	load()
	a.pushNative(parent, w)
}

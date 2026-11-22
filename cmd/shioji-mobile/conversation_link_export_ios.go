package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type mobileLinkExport struct {
	id     string
	cancel context.CancelFunc
}

func mobileDownloadableURL(dest string) bool {
	u, err := url.Parse(dest)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func (a *mobileApp) exportConversationLink(parent *mygo.Window, source domain.Session, resource string, web bool) {
	root := a.splitRoot()
	if !a.visibleLinkSource(parent, source) || a.preview || root.linkExport != nil {
		return
	}
	model, controller := a.model, a.controller
	device := model.Snapshot.DeviceID
	if !model.Connected || controller == nil || device == "" ||
		source.Context.DeviceID != "" && source.Context.DeviceID != device {
		a.linkError(errors.New("连接会话所属设备后再保存链接"))
		return
	}
	epoch, generation := model.ConnectionEpoch, model.Snapshot.Generation
	target := app.RemoteAttachmentTarget(device, source.WorkspaceID, controller.DialDeviceStream)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	export := &mobileLinkExport{id: domain.NewID(), cancel: cancel}
	root.linkExport = export
	unregister := parent.OnClosed(cancel)
	window := parent.NativeHandle()
	visible := func() bool {
		return a.model == model && a.controller == controller && model.ConnectionEpoch == epoch &&
			model.Snapshot.Generation == generation && a.visibleLinkSource(parent, source)
	}
	go func() {
		start, done := context.WithTimeout(ctx, 5*time.Second)
		beginErr := uikit.Call(start, "beginLinkExport", map[string]any{"window": window, "id": export.id}, nil)
		done()
		var dir, file string
		err := beginErr
		if beginErr == nil {
			dir, file, err = downloadMobileLink(ctx, target, source.ID, resource, web)
		}
		mygo.RunOnMain(func() {
			args := map[string]string{"id": export.id}
			if visible() && ctx.Err() != context.Canceled {
				switch {
				case err == nil:
					args["path"] = file
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					args["problem"] = "读取链接超时，请重试"
				default:
					args["problem"] = err.Error()
				}
			}
			go func() {
				// Keep the completed source until the system reports that the
				// picker saved a copy or was dismissed.
				result := uikit.Call(context.Background(), "finishLinkExport", args, nil)
				if dir != "" {
					_ = os.RemoveAll(dir)
				}
				mygo.RunOnMain(func() {
					defer cancel()
					defer unregister()
					root.linkExport = nil
					if result == nil {
						result = beginErr
					}
					if result != nil && ctx.Err() != context.Canceled && visible() {
						a.linkError(result)
					}
				})
			}()
		})
	}()
}

func downloadMobileLink(ctx context.Context, target app.AttachmentTarget, session, resource string, web bool) (dir, path string, err error) {
	dir, err = os.MkdirTemp("", "shioji-link-export-")
	if err != nil {
		return "", "", err
	}
	var file app.LinkedFile
	if web {
		file, err = target.FetchURL(ctx, dir, resource, nil)
	} else {
		file, err = target.FetchFile(ctx, dir, session, resource, nil)
	}
	if err != nil {
		return dir, "", err
	}
	if filepath.Base(file.Path) == file.Resource.Name {
		return dir, file.Path, nil
	}
	// The file cache may give a reserved manifest name a distinct basename.
	path = filepath.Join(dir, file.Resource.Name)
	return dir, path, os.Rename(file.Path, path)
}

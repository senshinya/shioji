package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type mobileLegacyReceipt struct {
	Endpoint mobileclient.Endpoint       `json:"endpoint"`
	Build    string                      `json:"helperBuild"`
	Input    domain.WorkerMigrationInput `json:"input"`
}

func (a *mobileApp) loadLegacyMigrations() {
	var records map[string]mobileLegacyReceipt
	raw, err := os.ReadFile(filepath.Join(a.rootDirectory, "worker-legacy-migrations.json"))
	if os.IsNotExist(err) {
		return
	}
	if err == nil {
		err = json.Unmarshal(raw, &records)
	}
	if err != nil {
		a.legacyMigrationLoadError = errors.New("无法读取本机保存的迁移记录")
		return
	}
	for device, receipt := range records {
		_, buildErr := transport.StagedWorkerPath("/", receipt.Build)
		if device == "" || receipt.Endpoint.DeviceID != device || receipt.Input.DeviceID != device ||
			receipt.Input.ID == "" || receipt.Input.OwnerRef == "" || receipt.Endpoint.Validate() != nil || buildErr != nil {
			a.legacyMigrationLoadError = errors.New("本机保存的迁移记录不完整")
			return
		}
	}
	a.legacyMigrations = records
}

func (a *mobileApp) saveLegacyMigration(device string, receipt *mobileLegacyReceipt) error {
	if a.legacyMigrationLoadError != nil {
		return a.legacyMigrationLoadError
	}
	if a.rootDirectory == "" {
		return errors.New("本机无法保存迁移回执")
	}
	next := maps.Clone(a.legacyMigrations)
	if next == nil {
		next = make(map[string]mobileLegacyReceipt)
	}
	if receipt == nil {
		delete(next, device)
	} else {
		next[device] = *receipt
	}
	data, err := json.Marshal(next)
	if err == nil {
		err = app.WriteState(filepath.Join(a.rootDirectory, "worker-legacy-migrations.json"), data)
	}
	if err == nil {
		a.legacyMigrations = next
	}
	return err
}

func (a *mobileApp) legacyMigrationRows(c *ui.Context) {
	for _, device := range slices.Sorted(maps.Keys(a.legacyMigrations)) {
		receipt := a.legacyMigrations[device]
		name := a.savedDevice(device).Name
		if name == "" {
			name = deviceHost(receipt.Endpoint)
		}
		mobileRow(c.Key("legacy-migration:"+device), "执行服务迁移", name, "", serverSymbol, func() {
			a.openLegacyMigration(a.devices, receipt.Endpoint, transport.WorkerInfo{})
		})
	}
	if a.legacyMigrationLoadError != nil {
		label(c, a.legacyMigrationLoadError.Error(), captionTextSize, palette(c).warning).Role(ui.RoleStatus)
	}
}

func (a *mobileApp) openLegacyMigration(parent *mygo.Window, endpoint mobileclient.Endpoint, info transport.WorkerInfo) {
	device := endpoint.DeviceID
	if device == "" || !a.pageCurrent(parent) {
		return
	}
	receipt, exists := a.legacyMigrations[device]
	if !exists {
		receipt.Endpoint = endpoint
	}
	known := a.savedDevice(device).Endpoint.DeviceID == device
	name := a.savedDevice(device).Name
	if name == "" {
		name = deviceHost(receipt.Endpoint)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var w *mygo.Window
	progress := info.Migration
	if exists {
		progress = nil
	}
	var verified mobileclient.Endpoint
	var next time.Time
	req := mobileRequest{}
	retry := ""
	legacy := info.Legacy

	adopt := func(state domain.WorkerMigrationStatus) error {
		input := domain.WorkerMigrationInput{ID: state.ID, DeviceID: state.DeviceID, OwnerRef: state.OwnerRef}
		if state.ID == receipt.Input.ID {
			if input.OwnerRef != "" && input.OwnerRef != receipt.Input.OwnerRef {
				return errors.New("迁移回执的旧服务身份已改变")
			}
			input.OwnerRef = receipt.Input.OwnerRef
		}
		if input.DeviceID != device || input.ID == "" || input.OwnerRef == "" {
			return errors.New("迁移回执与指定设备或操作不一致")
		}
		if input != receipt.Input {
			update := receipt
			update.Input = input
			if err := a.saveLegacyMigration(device, &update); err != nil {
				return err
			}
			receipt = update
		}
		progress = &state
		return nil
	}
	run := func(action string) {
		if req.busy || !a.pageCurrent(w) || ctx.Err() != nil {
			return
		}
		req.err = ""
		signer, err := a.identity()
		if err != nil {
			req.err = err.Error()
			w.Invalidate()
			return
		}
		if action == "start" || action == "resume" {
			updated := receipt
			if action == "start" {
				if legacy == nil || !legacy.Supported || legacy.Busy || legacy.OwnerRef == "" || receipt.Input.ID != "" {
					return
				}
				updated.Input = domain.WorkerMigrationInput{ID: domain.NewID(), DeviceID: device, OwnerRef: legacy.OwnerRef}
			}
			if err = a.saveLegacyMigration(device, &updated); err != nil {
				req.err = err.Error()
				w.Invalidate()
				return
			}
			receipt, progress = updated, nil
			a.devices.Invalidate()
		}
		req.busy, retry = true, ""
		w.Invalidate()
		request, route := receipt, a.routeDial()
		go func() {
			timeout := 15 * time.Second
			if action == "prepare" || action == "start" || action == "resume" {
				timeout = 2 * time.Minute
			}
			call, stop := context.WithTimeout(ctx, timeout)
			defer stop()
			var inspected transport.WorkerInfo
			var result domain.WorkerMigrationStatus
			var connected mobileclient.Endpoint
			remote := request.Endpoint.WorkerSSH(signer, route)
			var err error
			switch action {
			case "prepare", "inspect":
				if action == "prepare" {
					var path string
					path, err = bundledWorkerPath()
					if err == nil {
						var build domain.WorkerBuild
						build, err = mobileclient.InspectWorkerArtifact(path)
						if err == nil {
							request.Build = build.BuildID
							err = remote.Stage(call, request.Build, bundledWorkerArtifact)
						}
					}
				}
				if err == nil {
					inspected, err = remote.InspectLegacyMigration(call, request.Build, device)
				}
			case "verify":
				connected, inspected, err = request.Endpoint.Bootstrap(call, signer, route, nil)
				if err == nil && (connected.DeviceID != device || inspected.Legacy != nil || inspected.Migration != nil) {
					err = errors.New("新版执行服务尚未通过设备身份核对")
				}
			default:
				result, err = remote.LegacyMigration(call, request.Build, action, request.Input)
			}
			mygo.RunOnMain(func() {
				if ctx.Err() != nil || !a.pageCurrent(w) {
					return
				}
				req.busy = false
				next = time.Now().Add(2 * time.Second)
				switch {
				case err != nil:
					retry = action
					if action == "start" || action == "resume" {
						retry, next = "", time.Now()
					}
					var problem *protocol.RemoteError
					if action == "status" && errors.As(err, &problem) && problem.Code == "notFound" {
						retry = "inspect"
						err = errors.New("未找到这次迁移记录，请重新检查设备")
					}
				case action == "prepare" || action == "inspect":
					receipt.Build = request.Build
					receipt.Endpoint.StateDirectory, receipt.Endpoint.WorkerPath = inspected.Directory, inspected.Worker
					legacy = inspected.Legacy
					if inspected.Migration != nil {
						err = adopt(*inspected.Migration)
					} else if legacy != nil {
						if receipt.Input.ID != "" {
							err = a.saveLegacyMigration(device, nil)
							if err == nil {
								receipt.Input = domain.WorkerMigrationInput{}
							}
						}
						progress = nil
					} else {
						// A second client may have completed the upgrade while this
						// page was closed. Confirm the new service over the same route.
						progress = &domain.WorkerMigrationStatus{Phase: domain.MigrationCompleted}
						next = time.Now()
					}
					if err != nil {
						retry = "inspect"
					}
				case action == "verify":
					if err = a.rememberDevice(connected, false); err == nil && receipt.Input.ID != "" {
						err = a.saveLegacyMigration(device, nil)
					}
					if err == nil {
						verified = connected
					} else {
						retry = "verify"
					}
				default:
					err = adopt(result)
					if err != nil {
						retry = "status"
					}
				}
				if err != nil {
					req.err = mobileclient.RedactOperationDiagnostics(err.Error())
				}
				a.devices.Invalidate()
				w.Invalidate()
			})
		}()
	}
	confirm := func() {
		req.busy = true
		w.Invalidate()
		go func() {
			result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: w, Type: mygo.MessageWarning,
				Message: "停止旧版服务并迁移？", Detail: "迁移会备份设备数据，并保留会话和暂停的待发消息。",
				Buttons: []string{"取消", "开始迁移"}, DestructiveButtons: []int{1}})
			mygo.RunOnMain(func() {
				if !a.pageCurrent(w) || ctx.Err() != nil {
					return
				}
				req.busy = false
				if err == nil && result.Button == 1 {
					run("start")
				} else {
					w.Invalidate()
				}
			})
		}()
	}
	w = a.page("执行服务迁移", func(c *ui.Context) {
		title, detail := legacyMigrationStatus(legacy, progress, verified.DeviceID != "")
		if receipt.Input.ID != "" && retry != "" && progress == nil {
			title = "迁移状态待核对"
		}
		if receipt.Build != "" && !req.busy && retry == "" && verified.DeviceID == "" {
			automatic := ""
			switch {
			case progress != nil && progress.Phase == domain.MigrationCompleted:
				automatic = "verify"
			case progress != nil && progress.Phase.Active() || progress == nil && receipt.Input.ID != "":
				automatic = "status"
			case receipt.Build != "" && legacy != nil && legacy.Busy && progress == nil:
				automatic = "inspect"
			}
			if automatic != "" {
				if delay := next.Sub(c.Now()); delay > 0 {
					c.After(delay)
				} else {
					run(automatic)
				}
			}
		}
		workerSetupView(c, name, receipt.Endpoint.Address, title, detail, &req, nil, func() {
			switch {
			case verified.DeviceID != "":
				primaryAction(c, "连接设备", func() {
					if err := a.switchSavedDevice(verified); err != nil {
						req.err = err.Error()
						w.Invalidate()
						return
					}
					a.resetDeviceNavigation(func(err error) {
						if err != nil || a.endpoint.DeviceID != device {
							return
						}
						if known {
							a.openDevice(a.devices)
						} else {
							a.openRemoteAgents(a.devices)
						}
					})
				})
			case req.busy:
				primaryAction(c, "正在核对执行服务", nil).Disabled(true)
			case retry != "":
				action := retry
				primaryAction(c, legacyMigrationRetryLabel(action), func() { run(action) })
			case receipt.Build == "":
				primaryAction(c, "检查旧版服务", func() { run("prepare") })
			case progress != nil && progress.CanResume:
				primaryAction(c, "继续迁移", func() { run("resume") })
			case progress != nil && (progress.Phase.Active() || progress.Phase == domain.MigrationCompleted) ||
				progress == nil && receipt.Input.ID != "":
				primaryAction(c, "正在核对迁移进度", nil).Disabled(true)
			case legacy != nil && legacy.Supported && !legacy.Busy && progress == nil:
				primaryAction(c, "停止旧版并迁移", confirm).Disabled(a.legacyMigrationLoadError != nil)
			default:
				primaryAction(c, "重新检查", func() { run("inspect") })
			}
		})
	})
	w.OnClosed(cancel)
	a.pushNative(parent, w)
}

func legacyMigrationRetryLabel(action string) string {
	switch action {
	case "status":
		return "重新查询迁移进度"
	case "verify":
		return "重新核对新版服务"
	default:
		return "重新检查"
	}
}

func legacyMigrationStatus(legacy *domain.LegacyWorkerInfo, state *domain.WorkerMigrationStatus, verified bool) (string, string) {
	if verified {
		return "迁移已完成", "已核对新版服务的设备身份。"
	}
	if state != nil {
		title := "正在准备迁移"
		switch state.Phase {
		case domain.MigrationStopping:
			title = "正在停止旧版服务"
		case domain.MigrationBackingUp:
			title = "正在备份会话"
		case domain.MigrationStarting:
			title = "正在启动新版服务"
		case domain.MigrationCompleted, domain.MigrationVerifying:
			title = "正在核对新版服务"
		case domain.MigrationFailed:
			title = "迁移未完成"
		case domain.MigrationBlocked:
			title = "等待任务结束"
		}
		return title, mobileclient.RedactOperationDiagnostics(state.Message)
	}
	if legacy == nil {
		return "正在读取迁移记录", ""
	}
	if legacy.Busy {
		return "等待任务结束", mobileclient.RedactOperationDiagnostics(legacy.Message)
	}
	if legacy.OwnerRef != "" && !legacy.Supported {
		return "此版本无法自动迁移", mobileclient.RedactOperationDiagnostics(legacy.Message)
	}
	return "检测到旧版执行服务", mobileclient.RedactOperationDiagnostics(legacy.Message)
}

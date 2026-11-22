package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
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

type mobileServicePreparation struct {
	Endpoint    mobileclient.Endpoint  `json:"endpoint"`
	Preparation app.ServicePreparation `json:"preparation"`
}

func planMobileServicePreparation(info transport.WorkerInfo) (*app.ServicePreparation, error) {
	path, err := bundledWorkerPath()
	if err != nil {
		return nil, err
	}
	candidate, err := mobileclient.InspectWorkerArtifact(path)
	if err != nil {
		return nil, err
	}
	preparation, err := app.PlanServicePreparation(info, candidate)
	if err == nil && preparation == nil {
		err = errors.New("执行服务尚未提供 Agent 配置功能，请重新检查设备")
	}
	return preparation, err
}

func (a *mobileApp) workerPreparationPath() string {
	return filepath.Join(a.rootDirectory, "worker-service-preparations.json")
}

func (a *mobileApp) loadWorkerPreparations() {
	raw, err := os.ReadFile(a.workerPreparationPath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var records map[string]mobileServicePreparation
	if err == nil {
		err = json.Unmarshal(raw, &records)
	}
	if err != nil {
		a.workerPreparationError = errors.New("无法读取本机保存的执行服务准备记录")
		return
	}
	for device, record := range records {
		p, endpoint := record.Preparation, record.Endpoint
		_, buildErr := transport.StagedWorkerPath(p.Info.Directory, p.Candidate.BuildID)
		if device == "" || p.ID == "" || p.Info.DeviceID != device || endpoint.DeviceID != device ||
			!filepath.IsAbs(p.Info.Directory) || endpoint.StateDirectory != p.Info.Directory || endpoint.WorkerPath != p.Info.Worker ||
			p.Info.Build == nil || p.Info.Build.BuildID == "" || endpoint.Validate() != nil || buildErr != nil {
			a.workerPreparationError = errors.New("本机保存的执行服务准备记录不完整")
			return
		}
	}
	a.workerPreparations = records
}

func (a *mobileApp) persistWorkerPreparation(record mobileServicePreparation, remove bool) error {
	if a.workerPreparationError != nil {
		return a.workerPreparationError
	}
	if a.rootDirectory == "" {
		return errors.New("本机无法保存执行服务准备记录")
	}
	device := record.Endpoint.DeviceID
	if existing, ok := a.workerPreparations[device]; ok && existing.Preparation.ID != record.Preparation.ID {
		return errors.New("设备已有其他准备记录，请打开原记录")
	}
	records := maps.Clone(a.workerPreparations)
	if records == nil {
		records = make(map[string]mobileServicePreparation)
	}
	if remove {
		delete(records, device)
	} else {
		records[device] = record
	}
	raw, err := json.Marshal(records)
	if err == nil {
		err = app.WriteState(a.workerPreparationPath(), raw)
	}
	if err == nil {
		a.workerPreparations = records
		a.devices.Invalidate()
	}
	return err
}

func (a *mobileApp) servicePreparationRows(c *ui.Context) {
	for _, device := range slices.Sorted(maps.Keys(a.workerPreparations)) {
		record := a.workerPreparations[device]
		name := a.savedDevice(device).Name
		if name == "" {
			name = record.Endpoint.Address
		}
		mobileRow(c.Key("worker-preparation:"+device), "执行服务准备", name, "", serverSymbol, func() {
			a.openServicePreparation(a.devices, record)
		})
	}
	if a.workerPreparationError != nil {
		label(c, a.workerPreparationError.Error(), captionTextSize, palette(c).warning).Role(ui.RoleStatus)
	}
}

func (a *mobileApp) openServicePreparation(parent *mygo.Window, record mobileServicePreparation) {
	if !a.pageCurrent(parent) || record.Endpoint.DeviceID == "" {
		return
	}
	device := record.Endpoint.DeviceID
	saved, started := a.workerPreparations[device]
	if started {
		record = saved
	}
	ctx, cancel := context.WithCancel(context.Background())
	var w *mygo.Window
	var next time.Time
	var verified mobileclient.Endpoint
	var waiting bool
	req := mobileRequest{}
	run := func(action string) {
		if req.busy || !a.pageCurrent(w) || ctx.Err() != nil {
			return
		}
		signer, err := a.identity()
		if err != nil {
			req.err = err.Error()
			w.Invalidate()
			return
		}
		request := record
		p := &request.Preparation
		waiting = p.Phase == app.ServiceWaiting
		if action == "update" {
			p.Phase, p.RequestedAt = app.ServiceDispatching, time.Now()
		}
		if !started || action == "update" {
			if err = a.persistWorkerPreparation(request, false); err != nil {
				req.err = err.Error()
				w.Invalidate()
				return
			}
			started, record = true, request
		}
		req.busy, req.err = true, ""
		w.Invalidate()
		route := a.routeDial()
		go func() {
			call, stop := context.WithTimeout(ctx, 45*time.Second)
			defer stop()
			remote := request.Endpoint.WorkerSSH(signer, route)
			result, err := app.RunServicePreparation(call, app.PrepareService{Action: action, Preparation: *p}, func(ctx context.Context) (net.Conn, error) {
				return request.Endpoint.DialRoute(ctx, signer, route)
			}, func(ctx context.Context) (domain.WorkerBuild, error) {
				return p.Candidate, remote.Stage(ctx, p.Candidate.BuildID, bundledWorkerArtifact)
			})
			mygo.RunOnMain(func() {
				if ctx.Err() != nil || !a.pageCurrent(w) {
					return
				}
				req.busy = false
				next = time.Now().Add(time.Second)
				update := request
				if err != nil {
					var refusal *protocol.RemoteError
					notSent := errors.As(err, &refusal) && refusal.Code == "notSent"
					update.Preparation.Phase = app.ServiceInterrupted
					update.Preparation.Message = mobileclient.RedactOperationDiagnostics(err.Error())
					if action == "observe" && notSent && !p.RequestedAt.IsZero() &&
						time.Since(p.RequestedAt) < app.ServicePreparationObservationLimit {
						// The old bridge can close before the new supervisor serves the socket.
						update.Preparation.Phase = app.ServiceObserving
						update.Preparation.Message = ""
					} else if action == "stage" || action == "update" && refusal != nil {
						update.Preparation.Phase = app.ServiceFailed
					}
				} else if result.Check != nil && result.Preparation.Phase == app.ServiceReady {
					ready := request.Endpoint
					if result.Check.Info.Build.BuildID == p.Candidate.BuildID {
						ready.WorkerPath, err = transport.StagedWorkerPath(ready.StateDirectory, p.Candidate.BuildID)
					}
					if err == nil {
						err = a.rememberDevice(ready, false)
					}
					if err == nil {
						err = a.persistWorkerPreparation(request, true)
					}
					if err == nil {
						verified = ready
					} else {
						req.err = err.Error()
					}
					w.Invalidate()
					return
				} else {
					update.Preparation = result.Preparation
				}
				if err := a.persistWorkerPreparation(update, false); err != nil {
					req.err = err.Error()
				} else {
					record = update
				}
				w.Invalidate()
			})
		}()
	}
	name, scope := a.savedDevice(device).Name, record.Endpoint.Address
	if name == "" {
		name, scope = scope, ""
	}
	w = a.page("执行服务准备", func(c *ui.Context) {
		p := record.Preparation
		if started && verified.DeviceID == "" && !req.busy && req.err == "" {
			if action := p.NextAction(); action != "" {
				if delay := next.Sub(c.Now()); delay > 0 {
					c.After(delay)
				} else {
					run(action)
				}
			}
		}
		title, detail := mobileServicePreparationStatus(p, started, waiting && req.busy, verified.DeviceID != "")
		workerSetupView(c, name, scope, title, detail, &req, nil, func() {
			switch {
			case verified.DeviceID != "":
				primaryAction(c, "配置远程 Agent", func() { a.configureVerifiedRemoteAgent(w, verified, &req) })
			case !started:
				primaryAction(c, "准备更新", func() { run("stage") }).Disabled(a.workerPreparationError != nil)
			case req.busy:
				primaryAction(c, "正在准备", nil).Disabled(true)
			case p.Phase == app.ServiceInterrupted:
				primaryAction(c, "重新核对", func() {
					record.Preparation.Phase = app.ServiceObserving
					record.Preparation.RequestedAt = time.Now()
					run("observe")
				})
			case req.err != "" && p.NextAction() != "":
				retry := p.NextAction()
				primaryAction(c, "重试", func() { run(retry) })
			case p.Phase == app.ServiceFailed:
				primaryAction(c, "重新检查设备", func() {
					if err := a.persistWorkerPreparation(record, true); err != nil {
						req.err = err.Error()
						w.Invalidate()
						return
					}
					w.Pop()
				})
			default:
				primaryAction(c, "正在准备", nil).Disabled(true)
			}
		})
	})
	w.OnClosed(cancel)
	a.pushNative(parent, w)
}

func mobileServicePreparationStatus(p app.ServicePreparation, started, waiting, verified bool) (string, string) {
	if verified {
		return "执行服务已就绪", "已核对新版执行服务和 Agent 目录。"
	}
	if !started {
		return "执行服务需要更新", "设备空闲后会备份数据并更新执行服务，完成后即可配置远程 Agent。"
	}
	if waiting || p.Phase == app.ServiceWaiting {
		return "等待设备空闲", mobileclient.RedactOperationDiagnostics(p.Message)
	}
	title := "正在准备更新"
	switch p.Phase {
	case app.ServiceDispatching:
		title = "正在更新执行服务"
	case app.ServiceObserving, app.ServiceReconnecting:
		title = "正在核对更新结果"
	case app.ServiceInterrupted:
		title = "更新结果待核对"
	case app.ServiceFailed:
		title = "执行服务准备未完成"
	}
	return title, mobileclient.RedactOperationDiagnostics(p.Message)
}

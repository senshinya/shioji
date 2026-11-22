package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobileSessionReload struct {
	intent     mobileclient.SessionReloadIntent
	controller *app.Controller
	epoch      string
	busy       bool
	message    string
	failed     bool
}

func (a *mobileApp) loadSessionReloads() {
	data, err := os.ReadFile(filepath.Join(a.rootDirectory, "session-reloads.json"))
	if os.IsNotExist(err) {
		return
	}
	var saved []mobileclient.SessionReloadIntent
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil {
		a.reloadStorageError = "无法读取待核对的重新加载操作（" + err.Error() + "）"
		return
	}
	for _, intent := range saved {
		if !intent.Valid() || a.sessionReloads[intent.Target] != nil {
			a.reloadStorageError = "保存的重新加载操作无法确认"
			return
		}
		a.sessionReloads[intent.Target] = &mobileSessionReload{intent: intent, message: "重新加载结果待核对"}
	}
}

func (a *mobileApp) saveSessionReloads() error {
	if a.preview {
		return nil
	}
	if a.rootDirectory == "" {
		return errors.New("无法访问应用数据目录")
	}
	saved := make([]mobileclient.SessionReloadIntent, 0, len(a.sessionReloads))
	for _, state := range a.sessionReloads {
		if state.intent.Request.OperationID != "" {
			saved = append(saved, state.intent)
		}
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return app.WriteState(filepath.Join(a.rootDirectory, "session-reloads.json"), data)
}

func (a *mobileApp) sessionReload(s domain.Session) *mobileSessionReload {
	target := mobileclient.ReloadTargetFor(a.model.Snapshot.DeviceID, s)
	state := a.sessionReloads[target]
	if state != nil && state.busy && (state.controller != a.controller || state.epoch != a.model.ConnectionEpoch || !a.model.Connected) {
		state.busy, state.failed, state.message = false, false, "连接已变化，重新加载结果待核对"
	}
	return state
}

func (a *mobileApp) reloadReason(s domain.Session) string {
	if a.reloadStorageError != "" {
		return a.reloadStorageError
	}
	return mobileclient.SessionReloadReason(a.model, s)
}

func (a *mobileApp) beginSessionReload(id string) {
	s := a.sessionByID(id)
	if a.reloadReason(s) != "" || !a.preview && a.controller == nil {
		return
	}
	if state := a.sessionReload(s); state != nil && state.intent.Request.OperationID != "" {
		return
	}
	target := mobileclient.ReloadTargetFor(a.model.Snapshot.DeviceID, s)
	state := &mobileSessionReload{intent: mobileclient.SessionReloadIntent{Target: target,
		Request: domain.SessionReloadRequest{OperationID: domain.NewID(), SessionID: s.ID, Revision: s.Revision}}}
	a.sessionReloads[target] = state
	if err := a.saveSessionReloads(); err != nil {
		state.intent.Request = domain.SessionReloadRequest{}
		state.failed, state.message = true, "无法保存重新加载操作（"+err.Error()+"）"
		a.refresh()
		return
	}
	a.sendSessionReload(state, false)
}

func (a *mobileApp) querySessionReload(id string) {
	state := a.sessionReload(a.sessionByID(id))
	if state != nil && state.intent.Valid() && !state.busy && a.model.Connected &&
		a.model.WorkerMinor >= protocol.SessionReloadMinor && a.reloadStorageError == "" {
		a.sendSessionReload(state, true)
	}
}

func (a *mobileApp) settleSessionReload(state *mobileSessionReload, message string, failed bool) {
	intent := state.intent
	state.intent.Request = domain.SessionReloadRequest{}
	if err := a.saveSessionReloads(); err != nil {
		state.intent = intent
		message, failed = "无法保存核对结果（"+err.Error()+"）", true
	}
	state.message, state.failed = message, failed
}

func (a *mobileApp) sendSessionReload(state *mobileSessionReload, query bool) {
	intent := state.intent
	m, controller := a.model, a.controller
	epoch := m.ConnectionEpoch
	state.controller, state.epoch = controller, epoch
	state.busy, state.failed, state.message = true, false, "正在重新加载…"
	method := "session.reload"
	if query {
		state.message = "正在核对重新加载结果…"
		method = "session.reload.operation"
	}
	done := func(data json.RawMessage, err error) {
		s := a.sessionByID(intent.Target.SessionID)
		if a.model != m || a.controller != controller || m.ConnectionEpoch != epoch ||
			intent.Target != mobileclient.ReloadTargetFor(m.Snapshot.DeviceID, s) ||
			a.sessionReloads[intent.Target] != state || !state.busy || state.intent != intent {
			return
		}
		defer a.refresh()
		state.busy = false
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (!query && remote.Code == "notSent" || query && remote.Code == "notFound") {
				message := "重新加载未开始（" + err.Error() + "）"
				if query {
					message = "执行服务未找到此次重新加载的回执"
				}
				a.settleSessionReload(state, message, true)
				return
			}
			state.message = "重新加载结果待核对"
			return
		}
		var result domain.SessionReloadResult
		if json.Unmarshal(data, &result) != nil || !intent.Accepts(result) {
			state.failed, state.message = true, "重新加载回执与原操作不一致"
			return
		}
		if result.Operation.State == "running" {
			state.message = "执行服务仍在重新加载"
			return
		}
		if result.Session.Revision >= s.Revision {
			for i := range m.Snapshot.Sessions {
				if m.Snapshot.Sessions[i].ID == s.ID {
					m.Snapshot.Sessions[i] = result.Session
					break
				}
			}
			if detail, ok := m.Details[s.ID]; ok && detail.Session.Revision <= result.Session.Revision {
				detail.Session = result.Session
				m.Details[s.ID] = detail
			}
		}
		if result.Operation.State == "completed" {
			a.settleSessionReload(state, "会话已重新加载", false)
		} else {
			message := "重新加载失败"
			if result.Operation.Error != "" {
				message += "（" + result.Operation.Error + "）"
			}
			a.settleSessionReload(state, message, true)
		}
	}
	if a.preview {
		a.previewSessionReload(method, intent.Request, done)
	} else if controller == nil || !controller.Do(app.Job{Method: method, Input: intent.Request, ConnectionEpoch: epoch,
		Apply: func(_ *app.Model, data json.RawMessage, err error) { done(data, err) },
	}) {
		state.busy, state.failed, state.message = false, true, "设备繁忙，请重新核对"
		if !query {
			a.settleSessionReload(state, "设备繁忙，重新加载未开始", true)
		}
	}
	a.refresh()
}

func (a *mobileApp) sessionReloadRow(c *ui.Context, s domain.Session) {
	reason := a.reloadReason(s)
	state := a.sessionReload(s)
	pending := state != nil && state.intent.Request.OperationID != ""
	if pending {
		reason = ""
	}
	mobileDetailRow(c.Key("reload"), "重新加载", reason, "", terminalSymbol, nil, nil).
		Role(ui.RoleButton).Label("重新加载").OnClick(func() { a.beginSessionReload(s.ID) }).Disabled(pending || reason != "").Value(reason)
	if state == nil || state.message == "" {
		return
	}
	p := palette(c)
	ui.Column(c.Key("reload-feedback")).Padding(0, 0, 0, 30).Gap(1).Transition(mobilePanelTransition).Children(func() {
		color := p.secondary
		if state.failed {
			color = p.warning
		}
		label(c.Key("status"), state.message, captionTextSize, color).Padding(6, 0)
		if pending && !state.busy {
			action(c.Key("receipt"), "核对重新加载结果", func() { a.querySessionReload(s.ID) }).AlignSelf(ui.Start).PaddingX(0).
				Disabled(!a.model.Connected || a.model.WorkerMinor < protocol.SessionReloadMinor || a.reloadStorageError != "")
		}
	})
}

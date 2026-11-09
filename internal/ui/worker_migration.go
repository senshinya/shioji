package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"strconv"
	"strings"
	"time"
)

type migrationViewPhase uint8

const (
	migrationViewInspecting migrationViewPhase = iota
	migrationViewReady
	migrationViewFollowing
	migrationViewRechecking
	migrationViewError
)

type workerMigrationUI struct {
	target  app.WorkerMigrationTarget
	phase   migrationViewPhase
	pending bool
	next    time.Time
	err     string
}

func (v *View) migrationFlows() map[string]*workerMigrationUI {
	if v.workerMigrations == nil {
		v.workerMigrations = map[string]*workerMigrationUI{}
	}
	return v.workerMigrations
}
func (v *View) setupMigration() *workerMigrationUI {
	return v.migrationFlows()[v.sshSetup.Info.DeviceID]
}
func (v *View) migrationForConnection(key string) *workerMigrationUI {
	for _, flow := range v.migrationFlows() {
		if !flow.target.Setup && flow.target.ConnectionID == key {
			return flow
		}
	}
	return nil
}
func (v *View) migrationVisible(flow *workerMigrationUI) bool {
	return flow.target.Setup && v.welcome != nil && v.welcome.step == welcomeSSH && v.welcome.key == "" && flow.target.SSH != nil && strings.TrimSpace(v.sshTarget) == flow.target.SSH.Target && migrationPort(v.sshPort) == flow.target.SSH.Port && v.sshSetup.Info.DeviceID == flow.target.Info.DeviceID
}
func migrationPort(value string) int { port, _ := strconv.Atoi(strings.TrimSpace(value)); return port }
func (v *View) acceptSSHMigration(info transport.WorkerInfo) {
	port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
	target := app.WorkerMigrationTarget{Setup: true, Name: v.sshName, Info: info, SSH: &transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port, Worker: info.Worker, StateDirectory: info.Directory}}
	flow := &workerMigrationUI{target: target, phase: migrationViewReady}
	v.migrationFlows()[info.DeviceID] = flow
	v.sshCheck = sshLegacyUpgrade
	v.sshError = ""
	v.retireSSHSetupTerminal()
	if info.Migration != nil {
		v.adoptWorkerMigration(flow, *info.Migration)
	}
}
func (v *View) adoptWorkerMigration(flow *workerMigrationUI, state domain.WorkerMigrationStatus) {
	flow.target.Input = domain.WorkerMigrationInput{ID: state.ID, DeviceID: state.DeviceID, OwnerRef: state.OwnerRef}
	flow.target.Status = &state
	flow.target.Request = app.MigrationObserving
	flow.phase = migrationViewFollowing
	if state.Phase == domain.MigrationCompleted {
		flow.target.Request = app.MigrationResolved
	}
	if v.Model.WorkerMigrations == nil {
		v.Model.WorkerMigrations = map[string]app.WorkerMigrationTarget{}
	}
	v.Model.WorkerMigrations[state.DeviceID] = flow.target
	if v.Saver != nil {
		v.queueState()
	}
}
func (v *View) beginNormalMigration(key string) {
	target := app.WorkerMigrationTarget{ConnectionID: key, Name: v.Model.LocalName(), Info: transport.WorkerInfo{DeviceID: v.Model.ConnectionDeviceID(key)}}
	if key != "local" {
		for _, conn := range v.Model.Connections {
			if conn.ID == key {
				ssh := conn.SSH
				target.SSH = &ssh
				target.Name = conn.Name
				break
			}
		}
	}
	flow := &workerMigrationUI{target: target, phase: migrationViewInspecting}
	mapKey := target.Info.DeviceID
	if mapKey == "" {
		mapKey = "connection:" + key
	}
	v.migrationFlows()[mapKey] = flow
	v.inspectMigration(flow)
}
func (v *View) inspectMigration(flow *workerMigrationUI) {
	if flow.pending || v.Controller == nil {
		return
	}
	flow.pending = true
	if flow.phase != migrationViewReady {
		flow.phase = migrationViewInspecting
	}
	flow.err = ""
	target := flow.target
	if !v.Controller.Do(app.Job{ConnectionID: target.ConnectionID, Method: "$worker.migration.inspect", Input: app.WorkerMigrationAction{Action: "inspect", Target: target}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		flow.pending = false
		flow.next = v.frameTime.Add(time.Second)
		if err != nil {
			flow.phase = migrationViewError
			flow.err = err.Error()
			return
		}
		var info transport.WorkerInfo
		if json.Unmarshal(b, &info) != nil || info.DeviceID == "" || target.Info.DeviceID != "" && target.Info.DeviceID != info.DeviceID {
			flow.phase = migrationViewError
			flow.err = "服务检查回执的设备身份不一致"
			return
		}
		flow.target.Info = info
		for id, candidate := range v.migrationFlows() {
			if candidate == flow && id != info.DeviceID {
				delete(v.workerMigrations, id)
			}
		}
		v.migrationFlows()[info.DeviceID] = flow
		if info.Migration != nil {
			v.adoptWorkerMigration(flow, *info.Migration)
			return
		}
		if info.Legacy != nil {
			flow.phase = migrationViewReady
			flow.target.Status = nil
			flow.target.Input = domain.WorkerMigrationInput{}
			flow.target.Request = app.MigrationResolved
			delete(m.WorkerMigrations, info.DeviceID)
		} else {
			flow.phase = migrationViewRechecking
			delete(m.WorkerMigrations, info.DeviceID)
			if v.migrationVisible(flow) {
				v.sshSetup.Info = info
				v.checkSSHDevice()
			} else if !target.Setup {
				delete(v.workerMigrations, info.DeviceID)
				v.do(app.Job{ConnectionID: target.ConnectionID, Method: "$reconnect"})
			}
		}
		if info.Legacy != nil && target.Setup && v.migrationVisible(flow) {
			v.sshSetup.Info = info
			v.sshCheck = sshLegacyUpgrade
		}
		if v.Saver != nil {
			v.queueState()
		}
	}}) {
		flow.pending = false
		flow.phase = migrationViewError
		flow.err = "设备操作繁忙，请重试"
	}
}
func (v *View) requestMigration(flow *workerMigrationUI, action string) {
	if flow == nil || flow.pending || v.Controller == nil {
		return
	}
	if action == "start" {
		legacy := flow.target.Info.Legacy
		if legacy == nil || !legacy.Supported || legacy.Busy {
			return
		}
		flow.target.Input = domain.WorkerMigrationInput{ID: domain.NewID(), DeviceID: flow.target.Info.DeviceID, OwnerRef: legacy.OwnerRef}
		flow.target.Status = nil
		flow.target.Request = app.MigrationSubmitting
	}
	if action == "resume" {
		flow.target.Status = nil
		flow.target.Request = app.MigrationSubmitting
	}
	if v.Model.WorkerMigrations == nil {
		v.Model.WorkerMigrations = map[string]app.WorkerMigrationTarget{}
	}
	v.Model.WorkerMigrations[flow.target.Info.DeviceID] = flow.target
	var prepare func() error
	if action != "status" && v.Saver != nil {
		v.queueState()
		prepare = v.Saver.FlushLatest
	}
	target := flow.target
	flow.pending = true
	flow.phase = migrationViewFollowing
	flow.err = ""
	flow.next = v.frameTime.Add(time.Second)
	job := app.Job{ConnectionID: target.ConnectionID, Method: "$worker.migration", Input: app.WorkerMigrationAction{Action: action, Target: target}, Prepare: prepare, Apply: func(m *app.Model, b json.RawMessage, err error) {
		flow.pending = false
		flow.next = v.frameTime.Add(time.Second)
		if flow.target.Input.ID != target.Input.ID {
			return
		}
		if err != nil {
			flow.err = err.Error()
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (remote.Code == "notFound" || remote.Code == "notSent") {
				flow.phase = migrationViewError
				flow.target.Request = app.MigrationResolved
				if remote.Code == "notFound" {
					flow.err = "未找到这次服务准备记录，请重新检查设备状态"
				}
			}
			m.WorkerMigrations[target.Info.DeviceID] = flow.target
			if v.Saver != nil {
				v.queueState()
			}
			return
		}
		var state domain.WorkerMigrationStatus
		if json.Unmarshal(b, &state) != nil || state.DeviceID != target.Info.DeviceID || state.ID == "" {
			flow.err = "服务准备回执身份不一致"
			return
		}
		// Another client can already own this device's operation. Follow the
		// authoritative identifier without sending a second stop.
		v.adoptWorkerMigration(flow, state)
		if state.Phase == domain.MigrationBlocked {
			flow.phase = migrationViewReady
			if flow.target.Info.Legacy != nil {
				legacy := *flow.target.Info.Legacy
				legacy.Busy = true
				legacy.Message = state.Message
				flow.target.Info.Legacy = &legacy
			}
			delete(m.WorkerMigrations, state.DeviceID)
		}
	}}
	if !v.Controller.Do(job) {
		flow.pending = false
		flow.phase = migrationViewError
		flow.err = "设备操作繁忙，尚未发送准备请求"
		flow.target.Request = app.MigrationResolved
		v.Model.WorkerMigrations[target.Info.DeviceID] = flow.target
	}
	if v.migrationVisible(flow) && flow.phase == migrationViewFollowing {
		v.sshCheck = sshMigrating
	}
}
func migrationCaption(flow *workerMigrationUI) string {
	if flow == nil {
		return "需要更新设备服务"
	}
	if flow.phase == migrationViewInspecting {
		return "正在检查设备服务"
	}
	if flow.phase == migrationViewError {
		return "服务准备未完成"
	}
	if flow.phase == migrationViewFollowing && flow.target.Status == nil {
		return "正在准备设备服务"
	}
	if state := flow.target.Status; state != nil {
		switch state.Phase {
		case domain.MigrationStopping:
			return "正在停止旧版服务"
		case domain.MigrationBackingUp:
			return "正在备份会话"
		case domain.MigrationStarting:
			return "正在启动新版服务"
		case domain.MigrationVerifying:
			return "正在核对设备"
		case domain.MigrationCompleted:
			return "服务已更新"
		case domain.MigrationFailed:
			return "服务准备未完成"
		}
		if state.Phase.Active() {
			return "正在准备设备服务"
		}
	}
	if legacy := flow.target.Info.Legacy; legacy != nil && legacy.Busy {
		return "等待任务结束"
	}
	return "需要更新设备服务"
}
func migrationAction(flow *workerMigrationUI) (string, bool) {
	if flow == nil {
		return "准备更新", false
	}
	if flow.phase == migrationViewInspecting {
		return "正在检查服务", true
	}
	if flow.pending && flow.target.Status == nil && flow.phase == migrationViewFollowing {
		return migrationCaption(flow), true
	}
	if flow.phase == migrationViewError {
		return "重新检查", false
	}
	if state := flow.target.Status; state != nil {
		if state.Phase.Active() {
			return migrationCaption(flow), true
		}
		if state.Phase == domain.MigrationCompleted {
			return "正在连接", true
		}
		if state.Phase == domain.MigrationFailed {
			if state.CanResume {
				return "继续准备", false
			}
			return "重新检查", false
		}
	}
	if legacy := flow.target.Info.Legacy; legacy != nil {
		if legacy.Busy {
			return "等待任务结束", true
		}
		if !legacy.Supported {
			return "重新检查", false
		}
		return "停止旧版并升级", flow.pending
	}
	return "重新检查", false
}
func (v *View) performMigrationAction(flow *workerMigrationUI) {
	action, disabled := migrationAction(flow)
	if disabled {
		return
	}
	switch action {
	case "停止旧版并升级":
		v.requestMigration(flow, "start")
	case "继续准备":
		v.requestMigration(flow, "resume")
	default:
		v.inspectMigration(flow)
	}
}
func migrationMessage(flow *workerMigrationUI) string {
	if flow == nil {
		return ""
	}
	if flow.err != "" {
		return flow.err
	}
	if flow.target.Status != nil && flow.target.Status.Message != "" {
		return flow.target.Status.Message
	}
	if flow.target.Info.Legacy != nil {
		return flow.target.Info.Legacy.Message
	}
	return ""
}
func (v *View) sshMigrationContent(c *n.Context, t *n.Theme) {
	flow := v.setupMigration()
	visible := flow != nil && (v.sshCheck == sshLegacyUpgrade || v.sshCheck == sshMigrating)
	frame := n.Column(c.Key("worker-migration-content")).FillWidth().Clip()
	animatedContentHeight(c, frame, visible, 240*time.Millisecond, func() float32 {
		if flow == nil {
			return 0
		}
		n.Column(c).FillWidth().Padding(12, 0, 0).Gap(6).Children(func() {
			n.Text(c.Key("migration-progress"), migrationCaption(flow)).FontSize(13).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			if message := migrationMessage(flow); message != "" {
				n.Text(c, message).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond))
			}
		})
		return 0
	})
}
func (v *View) syncWorkerMigrations(c *n.Context) {
	if !v.workerMigrationRestored {
		v.workerMigrationRestored = true
		for id, target := range v.Model.WorkerMigrations {
			flow := &workerMigrationUI{target: target, phase: migrationViewFollowing}
			if target.Request == app.MigrationResolved && target.Status == nil {
				flow.phase = migrationViewError
				flow.err = "需要重新检查设备服务"
			}
			v.migrationFlows()[id] = flow
			if target.Setup && v.Model.FirstRun && v.setupFlow().step == welcomeDevice && target.SSH != nil {
				f := v.setupFlow()
				f.key = ""
				f.step = welcomeSSH
				v.sshTarget = target.SSH.Target
				v.sshName = target.Name
				v.sshPort = ""
				if target.SSH.Port != 0 {
					v.sshPort = strconv.Itoa(target.SSH.Port)
				}
				v.sshSetup = app.DeviceSetupCheck{Info: target.Info}
				v.sshCheck = sshMigrating
			}
		}
	}
	for id, flow := range v.migrationFlows() {
		if flow.pending {
			continue
		}
		if state := flow.target.Status; state != nil && state.Phase == domain.MigrationCompleted {
			if flow.target.Setup && v.migrationVisible(flow) && flow.phase != migrationViewRechecking {
				flow.phase = migrationViewRechecking
				v.checkSSHDevice()
			}
			if !flow.target.Setup {
				if flow.phase != migrationViewRechecking {
					flow.phase = migrationViewRechecking
					v.do(app.Job{ConnectionID: flow.target.ConnectionID, Method: "$reconnect"})
				}
				state := v.Model.DeviceStates[flow.target.ConnectionID]
				if state != nil && state.Connected && state.Snapshot.DeviceID == id {
					delete(v.Model.WorkerMigrations, id)
					delete(v.workerMigrations, id)
					if v.Saver != nil {
						v.queueState()
					}
				}
			}
			continue
		}
		if c.Now().Before(flow.next) {
			c.After(flow.next.Sub(c.Now()))
			continue
		}
		if flow.phase == migrationViewFollowing && flow.target.Input.ID != "" {
			if flow.target.Status != nil && flow.target.Status.Phase == domain.MigrationFailed && flow.target.Status.CanResume {
				continue
			}
			v.requestMigration(flow, "status")
			continue
		}
		if flow.phase == migrationViewReady && flow.target.Info.Legacy != nil && flow.target.Info.Legacy.Busy {
			flow.next = c.Now().Add(time.Second)
			v.inspectMigration(flow)
		}
	}
}

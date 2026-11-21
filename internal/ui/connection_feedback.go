package ui

import (
	"slices"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func connectionPhase(connected bool, phase app.ConnectionPhase, paths []app.DevicePathStatus) app.ConnectionPhase {
	hasActive := slices.ContainsFunc(paths, func(path app.DevicePathStatus) bool { return path.Phase == app.PathActive })
	if (!connected || !hasActive) && slices.ContainsFunc(paths, func(path app.DevicePathStatus) bool { return path.Phase == app.PathSwitching }) {
		return app.ConnectionSwitching
	}
	if phase == app.ConnectionIdle && !connected {
		return app.ConnectionOpening
	}
	return phase
}

func (v *View) currentConnectionPhase() app.ConnectionPhase {
	m := v.Model
	return connectionPhase(m.Connected, m.ConnectionPhase, m.DevicePaths)
}

func connectionPhaseLabel(phase app.ConnectionPhase) string {
	switch phase {
	case app.ConnectionOpening:
		return "正在连接"
	case app.ConnectionSwitching:
		return "正在切换线路"
	case app.ConnectionReconnecting:
		return "正在重连"
	case app.ConnectionSyncing:
		return "正在同步会话"
	case app.ConnectionSyncFailed:
		return "会话同步受阻"
	case app.ConnectionUnavailable:
		return "连接不可用"
	}
	return ""
}

func (v *View) connectionRecoveryAction() statusAction {
	if v.currentConnectionPhase() == app.ConnectionSyncFailed {
		return statusAction{label: "设备详情", run: func(*n.Context) { v.showFeedbackDevice() }}
	}
	return statusAction{label: "重试连接", disabled: v.Controller == nil, run: func(*n.Context) {
		v.do(app.Job{Method: "$reconnect"})
	}}
}

func (v *View) connectionBlocker() string {
	switch v.currentConnectionPhase() {
	case app.ConnectionUnavailable:
		return "暂时连不上 " + v.Model.CurrentDeviceName()
	case app.ConnectionSyncFailed:
		return "无法同步会话"
	}
	return ""
}

func (v *View) agentCheckPending() bool {
	m := v.Model
	return m.Connected && v.currentConnectionPhase() == app.ConnectionIdle &&
		(m.Readiness.State == domain.RuntimeRefreshing || m.RunReadiness().State == domain.RuntimeRefreshing ||
			m.Readiness.State == "" && len(m.Readiness.Harnesses) == 0 && m.Readiness.Error == "")
}

func (v *View) agentBlocker() (title, details string) {
	m := v.Model
	ready := m.RunReadiness()
	if !m.Connected || v.currentConnectionPhase() != app.ConnectionIdle || v.agentCheckPending() || ready.State == domain.RuntimeReady {
		return "", ""
	}
	for _, info := range m.Readiness.Harnesses {
		if info.ID != ready.HarnessID || info.Installation == nil {
			continue
		}
		switch info.Installation.Issue {
		case domain.RuntimeMissing:
			return "未安装 " + info.Name, ready.Error
		case domain.RuntimeSignInRequired, domain.RuntimeAuthenticationExpired:
			return info.Name + " 需要登录", ready.Error
		case domain.RuntimeIncompatible:
			return info.Name + " 版本不兼容", ready.Error
		}
	}
	if m.Readiness.State == "" && m.Readiness.Error != "" {
		return "无法读取 Agent 状态", m.Readiness.Error
	}
	return "当前 Agent 无法运行", ready.Error
}

package ui

import (
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strconv"
	"strings"
)

func runtimeSetupLabel(info domain.HarnessInfo, connected bool) string {
	if !connected {
		return "未连接"
	}
	if info.State == domain.RuntimeRefreshing {
		return "正在检测"
	}
	if info.Installation != nil {
		switch info.Installation.Issue {
		case domain.RuntimeMissing:
			return "未安装"
		case domain.RuntimeSignInRequired:
			return "需要登录"
		case domain.RuntimeAuthenticationExpired:
			return "登录已失效"
		case domain.RuntimeIncompatible:
			return "版本不兼容"
		}
	}
	if info.State == domain.RuntimeReady {
		return "已就绪"
	}
	return "未就绪"
}
func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func (v *View) agentGuidance(c *n.Context, key string, info domain.HarnessInfo) {
	install := info.Installation
	if install == nil {
		return
	}
	n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
		n.Text(c, install.Guide).Grow(1).MinWidth(0).FontSize(11).FixedLineHeight(18).TextColor(c.Theme().TextMuted)
		if len(install.SignInCommand) > 0 && (install.Issue == domain.RuntimeSignInRequired || install.Issue == domain.RuntimeAuthenticationExpired) && configurationAction(c, "复制登录命令").Shrink(0).Clicked() {
			command := v.agentLoginCommand(key, install.SignInCommand)
			c.WriteClipboard(command)
		}
		if install.Documentation != "" && configurationAction(c, "安装说明").Shrink(0).Clicked() {
			if err := mygo.Shell.OpenExternal(install.Documentation); err != nil {
				v.Model.Error = err.Error()
			}
		}
	})
}

func (v *View) agentLoginCommand(key string, argv []string) string {
	parts := []string{}
	for _, arg := range argv {
		parts = append(parts, shellArgument(arg))
	}
	command := strings.Join(parts, " ")
	target, port := "", 0
	if key == "" {
		target = strings.TrimSpace(v.sshTarget)
		port, _ = strconv.Atoi(strings.TrimSpace(v.sshPort))
	}
	for _, conn := range v.Model.Connections {
		if conn.ID == key {
			target, port = conn.SSH.Target, conn.SSH.Port
		}
	}
	if target != "" {
		prefix := "ssh -t "
		if port > 0 {
			prefix += "-p " + strconv.Itoa(port) + " "
		}
		command = prefix + "-- " + shellArgument(target) + " " + shellArgument(command)
	}
	return command
}

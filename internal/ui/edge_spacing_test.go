package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestModelHoverKeepsControlsAwayFromItsEdges(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	info := domain.HarnessInfo{ID: "custom", State: domain.RuntimeReady, Models: []domain.Model{{ID: "sol", Name: "GPT Sol"}}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(32).Children(func() {
			n.Column(c).FillWidth().Clip().Label("配置列表容器").Children(func() { v.deviceModels(c, c.Theme(), "local", info) })
		})
	}, 624, 240)
	ui.Advance(time.Second)
	row, _ := ui.Find("模型：GPT Sol")
	star, _ := ui.Find("收藏 GPT Sol")
	toggle, _ := ui.Find("在模型菜单显示 GPT Sol")
	viewport, _ := ui.Find("模型列表区域")
	clip, _ := ui.Find("配置列表容器")
	if star.X-max(row.X, clip.X) < 7.5 || min(row.X+row.W, clip.X+clip.W)-toggle.X-toggle.W < 7.5 {
		t.Fatal("model row hover hugs controls", row, star, toggle)
	}
	if row.X < viewport.X-.5 || row.X+row.W > viewport.X+viewport.W+.5 {
		t.Fatal("model hover padding clipped by viewport", row, viewport)
	}
}

func TestSkillAndMCPRowHoverKeepsReadingInset(t *testing.T) {
	v, _, ui := skillDeviceUI(t)
	job := welcomeJob(t, v.Controller, "runtime.skills.list")
	body, _ := json.Marshal(domain.SkillInventory{Revision: "one", Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "check", Name: "check"}, Description: "Review code", Scope: domain.SkillUser, Directory: "/skills", Enabled: true, CanEnable: true}}})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	hit, ok := ui.Find("查看 Skill：check")
	if !ok {
		t.Fatal("skill row missing")
	}
	if firstRowInk(ui, hit)-hit.X < 7.5 {
		t.Fatal("skill icon hugs hover edge", hit, firstRowInk(ui, hit))
	}
	v, ui = workspaceToolFixture(t)
	job = welcomeJob(t, v.Controller, "runtime.tools.inspect")
	body, _ = json.Marshal(workspaceToolCatalog())
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	hit, ok = ui.Find("查看 MCP：filesystem")
	if !ok {
		t.Fatal("MCP row missing")
	}
	if firstRowInk(ui, hit)-hit.X < 7.5 {
		t.Fatal("MCP icon hugs hover edge", hit, firstRowInk(ui, hit))
	}
}

func firstRowInk(ui *n.Tester, hit n.Rect) float32 {
	im := ui.Image()
	bg := im.RGBAAt(int(hit.X+1), int(hit.Y+1))
	for x := int(hit.X); x < int(hit.X+40); x++ {
		for y := int(hit.Y + 3); y < int(hit.Y+24); y++ {
			p := im.RGBAAt(x, y)
			if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
				return float32(x)
			}
		}
	}
	return -1
}

func TestNotificationActionsHaveSeparatePaddedHitTargets(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	s.page = "notifications"
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	first, a := ui.Find("发送测试通知")
	second, b := ui.Find("系统通知设置…")
	if !a || !b || second.X-(first.X+first.W) < 3.5 {
		t.Fatal("notification action targets overlap", first, second)
	}
}

func TestConfigurationDisclosureHoverClearsBothContentEdges(t *testing.T) {
	v := devicePathsView()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	head, _ := ui.Find("设备状态区域")
	path := v.Model.Connections[0].Routes[0].ID
	hit, _ := ui.Find("展开连接路径:" + path)
	icon, _ := ui.Find("路径名称:" + path)
	status, _ := ui.Find("路径状态:" + path)
	if icon.X-hit.X < 7.5 {
		t.Fatal("path hover hugs icon or shifts section baseline", head, hit, icon)
	}
	if hit.X+hit.W-status.X-status.W < 7.5 {
		t.Fatal("path hover hugs status", hit, status)
	}
	service, _ := ui.Find("服务详情")
	if math.Abs(float64(head.X-service.X-8)) > 1 {
		t.Fatal("disclosure hover has no left breathing room", head, service)
	}
}

func TestRemoteWorkspaceDirectoryActionsShareRightEdge(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.remoteWorkspaceOpen = true
	v.remoteWorkspaceConnection = "air"
	v.remoteWorkspaceExtra = "/Users/remote/extra"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	choose, a := ui.Find("选择主目录")
	add, b := ui.Find("添加文件夹")
	remove, d := ui.Find("移除附加目录：/Users/remote/extra")
	if !a || !b || !d {
		t.Fatal("folder action missing")
	}
	if math.Abs(float64(choose.X+choose.W-add.X-add.W)) > 1 || math.Abs(float64(choose.X+choose.W-remove.X-remove.W)) > 1 {
		t.Fatal("folder action edges differ", choose, add, remove)
	}
}

func TestSettingsLinkHoverHasPaddingWithoutMovingRowEdges(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	s.page = "shortcuts"
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	hit, _ := ui.Find("前往消息输入")
	text, _ := ui.Find("发送、换行与立即介入")
	if text.X-hit.X < 7.5 {
		t.Fatal("settings link hover hugs caption", hit, text)
	}
}

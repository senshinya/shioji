package ui

import (
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWelcomeCatalogArrivalMovesActionsWithActualHeight(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step = welcomeAgents
	f.agents = agentEditor(nil)
	f.agents.loading = true
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("继续")
	catalog := setupCatalog()
	other := catalog[0]
	other.ID = "second"
	other.Name = "Second Agent"
	f.agents.catalog = append(catalog, other)
	f.agents.loading = false
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("继续")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("继续")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("catalog arrival jumps actions", before, mid, end)
	}
}
func TestWelcomeBusyActionResizesSmoothlyWithFixedRightEdge(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.name = "Project"
	f.directory = "/Users/local/project"
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("下一步")
	f.busy = true
	ui.Frame()
	ui.Advance(40 * time.Millisecond)
	mid, _ := ui.Find("正在创建")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("正在创建")
	if mid.W >= before.W-1 || mid.W <= end.W+1 || math.Abs(float64(mid.X+mid.W-end.X-end.W)) > 1 {
		t.Fatal("busy action width jumps", before, mid, end)
	}
}
func TestModelCatalogGrowthResizesItsViewportSmoothly(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	info := setupCatalog()[0]
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(560).Children(func() { v.deviceModels(c, c.Theme(), "local", info, "A"); n.Text(c, "下方操作") })
	}, 1100, 600)
	ui.Advance(time.Second)
	before, _ := ui.Find("下方操作")
	info.Models = append(info.Models, domain.Model{ID: "two", Name: "Two"}, domain.Model{ID: "three", Name: "Three"})
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("下方操作")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("下方操作")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("catalog height jumps rather than growing", before, mid, end)
	}
}
func TestWelcomeLongConfigurationErrorDoesNotBounceFooter(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.agents = agentEditor(setupCatalog())
	f.agents.edit(f.agents.catalog[0])
	f.agents.tab = "高级"
	for i := 0; i < 14; i++ {
		f.agents.draft.environment = append(f.agents.draft.environment, runtimeEnvironmentDraft{id: i + 1})
	}
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("保存")
	f.agents.error = "无法保存配置"
	ui.Frame()
	first, _ := ui.Find("保存")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("保存")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("保存")
	err, visible := ui.Find("无法保存配置")
	if !visible || err.H <= 0 || err.Y < 0 || err.Y+err.H > end.Y {
		t.Fatal("configuration error must remain visible above actions", err, end)
	}
	low, high := math.Min(float64(before.Y), float64(end.Y))-1, math.Max(float64(before.Y), float64(end.Y))+1
	if float64(first.Y) < low || float64(first.Y) > high || float64(mid.Y) < low || float64(mid.Y) > high {
		t.Fatal("error reserves height before it is shown", before, first, mid, end)
	}
}
func TestWelcomeDisclosuresAndDirectoryMutationsFollowTheirHeight(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step = welcomeSSH
	f.key = ""
	v.sshCheck = sshEditing
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("连接并准备服务")
	ui.Click("连接选项")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("连接并准备服务")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("连接并准备服务")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("SSH disclosure height does not follow", before, mid, end)
	}
	v.welcomeStep(welcomeWorkspace, 1)
	f.key = "local"
	previousPicker := openWorkspaceDirectoryDialog
	openWorkspaceDirectoryDialog = func(mygo.OpenDialogOptions) ([]string, error) { return []string{"/Users/local/extra"}, nil }
	t.Cleanup(func() { openWorkspaceDirectoryDialog = previousPicker })
	ui.Frame()
	ui.Advance(time.Second)
	before, _ = ui.Find("下一步")
	ui.Click("添加文件夹")
	ui.Advance(60 * time.Millisecond)
	mid, _ = ui.Find("下一步")
	ui.Advance(300 * time.Millisecond)
	end, _ = ui.Find("下一步")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("directory insertion height does not follow", before, mid, end)
	}
	ui.Click("移除附加目录")
	ui.Advance(60 * time.Millisecond)
	mid, _ = ui.Find("下一步")
	ui.Advance(300 * time.Millisecond)
	last, _ := ui.Find("下一步")
	if mid.Y >= end.Y-1 || mid.Y <= last.Y+1 {
		t.Fatal("directory removal height does not follow", end, mid, last)
	}
}

func TestWelcomeSlidingPageKeepsItsTextOpaque(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := welcomeModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1100, 800)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		ui.Click("配置本机")
		ui.Advance(80 * time.Millisecond)
		r, _ := ui.Find("配置设备")
		sample := func(r n.Rect) int {
			extreme := 255
			if dark {
				extreme = 0
			}
			im := ui.Image()
			for y := max(0, int(r.Y)); y < min(im.Bounds().Dy(), int(r.Y+r.H)); y++ {
				for x := max(0, int(r.X)); x < min(im.Bounds().Dx(), int(r.X+130)); x++ {
					p := im.RGBAAt(x, y)
					value := int(p.R)
					if dark {
						extreme = max(extreme, value)
					} else {
						extreme = min(extreme, value)
					}
				}
			}
			return extreme
		}
		mid := sample(r)
		ui.Advance(300 * time.Millisecond)
		r, _ = ui.Find("配置设备")
		end := sample(r)
		if math.Abs(float64(mid-end)) > 4 {
			t.Fatal("step slide changes opacity", dark, mid, end)
		}
	}
}

func TestWelcomeAccountDisclosureMovesActionsContinuously(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	catalog := setupCatalog()
	catalog[0].Installation.SignInCommand = []string{"agent", "login"}
	catalog[0].Account = &domain.RuntimeAccount{Label: "Profile", Identity: "dev@example.test", State: "authenticated"}
	f.agents = agentEditor(catalog)
	f.agents.edit(catalog[0])
	f.agents.tab = "常规"
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("保存")
	ui.Click("管理账号")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("保存")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("保存")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("account disclosure height jumps", before, mid, end)
	}
}
func TestRuntimeEnvironmentMutationsMoveFollowingContentContinuously(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	e := agentEditor(setupCatalog())
	e.edit(e.catalog[0])
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(320).Children(func() { v.runtimeEnvironmentFields(c, c.Theme(), e, false); n.Text(c, "下方操作") })
	}, 600, 500)
	ui.Advance(time.Second)
	before, _ := ui.Find("下方操作")
	ui.Click("添加变量")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("下方操作")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("下方操作")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("environment insertion height jumps", before, mid, end)
	}
	ui.Click("删除环境变量")
	ui.Advance(60 * time.Millisecond)
	mid, _ = ui.Find("下方操作")
	ui.Advance(300 * time.Millisecond)
	last, _ := ui.Find("下方操作")
	if mid.Y >= end.Y-1 || mid.Y <= last.Y+1 {
		t.Fatal("environment removal height jumps", end, mid, last)
	}
}

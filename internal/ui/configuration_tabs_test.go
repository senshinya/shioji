package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWelcomeConfigurationTabsSlideInBothDirections(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.agents = agentEditor(setupCatalog())
	f.agents.edit(f.agents.catalog[0])
	f.agents.tab = "常规"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("高级"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(60 * time.Millisecond)
	mid, ok := ui.Find("启动参数")
	if !ok {
		t.Fatal(ui.Texts())
	}
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("启动参数")
	if mid.X <= end.X+1 {
		t.Fatal("forward tab content does not slide from the right", mid, end)
	}
	ui.Click("常规")
	ui.Advance(60 * time.Millisecond)
	mid, ok = ui.Find("可执行文件")
	if !ok {
		t.Fatal(ui.Texts())
	}
	ui.Advance(400 * time.Millisecond)
	end, _ = ui.Find("可执行文件")
	if mid.X >= end.X-1 && mid.W >= end.W-1 {
		t.Fatal("reverse tab content does not slide from the left", mid, end)
	}
}

func TestDeviceTabsSlideAndVerticalSettingsStayInstant(t *testing.T) {
	t.Run("device", func(t *testing.T) {
		v := New(deviceUIModel(), nil, "")
		v.devices = true
		v.deviceSelected = "local"
		ui := n.NewTester(v.View, 1240, 820)
		ui.Advance(time.Second)
		nav, _ := ui.Find("设备设置")
		ui.Click("设备设置")
		ui.Advance(60 * time.Millisecond)
		mid, _ := ui.Find("执行服务")
		stationary, _ := ui.Find("设备设置")
		ui.Advance(400 * time.Millisecond)
		end, _ := ui.Find("执行服务")
		if mid.X <= end.X+1 || nav != stationary {
			t.Fatal("device tab motion missing or navigation moved", mid, end, nav, stationary)
		}
	})
	t.Run("settings", func(t *testing.T) {
		s := NewSettings(New(layoutModel(), nil, ""))
		ui := n.NewTester(s.View, 968, 756)
		ui.Advance(time.Second)
		ui.Click("通知")
		ui.Advance(60 * time.Millisecond)
		mid, ok := ui.Find("系统通知")
		if !ok {
			t.Fatal(ui.Texts())
		}
		ui.Advance(400 * time.Millisecond)
		end, _ := ui.Find("系统通知")
		if mid.X != end.X || mid.Y != end.Y {
			t.Fatal("vertical settings categories unexpectedly animate", mid, end)
		}
	})
}

func TestSharedTabsMoveSelectionAndKeepSlidingContentOpaque(t *testing.T) {
	for _, style := range []configurationTabStyle{configurationUnderline, configurationSidebar, configurationSegmented} {
		selected := "a"
		items := []configurationTabItem{{ID: "a", Label: "常规"}, {ID: "b", Label: "其他设置"}}
		ui := n.NewTester(func(c *n.Context) {
			n.Column(c).Width(320).Children(func() {
				selected = configurationTabs(c.Key("tabs"), selected, items, style, false)
				configurationTabPanel(c.Key("panel"), selected, configurationTabIndex(items, selected), configurationPanelFit, func() {
					color := n.Hex("#0000ff")
					if selected == "b" {
						color = n.Hex("#ff0000")
					}
					n.Box(c).Size(40, 40).Background(color).Label("不透明内容")
				})
			})
		}, 400, 240)
		ui.Advance(time.Second)
		marker := func() float32 {
			im := ui.Image()
			lo, hi := 400, -1
			for i := 0; i < 320; i++ {
				x, y := i, 35
				if style == configurationSidebar {
					x, y = 300, i
					if i >= 68 {
						break
					}
				}
				if style == configurationSegmented {
					y = 4
					if i < 2 || i >= 318 {
						continue
					}
				}
				px := im.RGBAAt(x, y)
				hit := px.R < 120
				if style == configurationUnderline {
					for row := 32; row < 36; row++ {
						px := im.RGBAAt(i, row)
						hit = hit || px.R < 120
					}
				}
				if style == configurationSidebar {
					hit = px.R < 210
				}
				if style == configurationSegmented {
					hit = px.R > 250 && px.G > 250 && px.B > 250
				}
				if hit {
					lo = min(lo, i)
					hi = max(hi, i)
				}
			}
			if hi < 0 {
				t.Fatal("missing visible selection", style)
			}
			return float32(lo+hi) / 2
		}
		before := marker()
		ui.Click("其他设置")
		ui.Move(-10, -10)
		ui.Advance(60 * time.Millisecond)
		mid := marker()
		pane, _ := ui.Find("不透明内容")
		px := ui.Image().RGBAAt(int(pane.X+pane.W/2), int(pane.Y+pane.H/2))
		if pane.W > 1 && (px.R != 255 || px.G != 0 || px.B != 0) {
			t.Fatal("sliding panel faded", style, pane, px)
		}
		ui.Advance(400 * time.Millisecond)
		end := marker()
		a, m, z := before, mid, end
		if style == configurationSidebar {
			if m != z || m == a {
				t.Fatal("vertical category selection animated", a, m, z)
			}
		} else if m <= a+1 || m >= z-1 {
			t.Fatal("selection jumps instead of travelling", style, before, mid, end)
		}
		ui.Key(0, n.KeyHome)
		ui.Advance(400 * time.Millisecond)
		if selected != "a" {
			t.Fatal("native tab keyboard navigation lost", style, selected)
		}
	}
}

func TestTabPanelsReduceMotionAndRapidReversal(t *testing.T) {
	selected := "a"
	items := []configurationTabItem{{ID: "a", Label: "常规"}, {ID: "b", Label: "高级"}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(320).Children(func() {
			selected = configurationTabs(c.Key("tabs"), selected, items, configurationUnderline, false)
			configurationTabPanel(c.Key("body"), selected, configurationTabIndex(items, selected), configurationPanelFit, func() { n.Box(c).Size(40, 40).Label("当前内容-" + selected) })
		})
	}, 400, 200)
	ui.Advance(time.Second)
	ui.Click("高级")
	ui.Advance(60 * time.Millisecond)
	ui.Click("常规")
	ui.Advance(30 * time.Millisecond)
	geometry, ok := ui.Find("当前内容-a")
	if !ok || geometry.X > 1 || geometry.W >= 39 {
		t.Fatal("reverse selection does not continue the visible motion", geometry)
	}
	ui.Advance(time.Second)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Click("高级")
	ui.Frame()
	geometry, _ = ui.Find("当前内容-b")
	if geometry.X != 0 || geometry.W != 40 {
		t.Fatal("reduced motion still slides the page", geometry)
	}
}

func TestFittedTabHeightMovesFooterWithContent(t *testing.T) {
	selected := "short"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(320).Children(func() {
			index, height := 0, float32(40)
			if selected == "tall" {
				index, height = 1, 160
			}
			configurationTabPanel(c.Key("panel"), selected, index, configurationPanelFit, func() { n.Box(c).Height(height).FillWidth() })
			n.Text(c, "下方操作")
		})
	}, 400, 300)
	ui.Advance(time.Second)
	before, _ := ui.Find("下方操作")
	selected = "tall"
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("下方操作")
	ui.Advance(time.Second)
	end, _ := ui.Find("下方操作")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("fitted page height snaps footer", before, mid, end)
	}
}

func TestSegmentedSelectionNeverCoversItsText(t *testing.T) {
	selected := "account"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(320).Children(func() {
			selected = configurationTabs(c, selected, []configurationTabItem{{ID: "account", Label: "账号授权"}, {ID: "api", Label: "自定义 API"}}, configurationSegmented, false)
		})
	}, 400, 100)
	ui.Advance(time.Second)
	ui.Click("自定义 API")
	ui.Move(-10, -10)
	ui.Advance(time.Second)
	r, _ := ui.Find("自定义 API")
	im := ui.Image()
	ink := 0
	for y := int(r.Y) + 3; y < int(r.Y+r.H)-3; y++ {
		for x := int(r.X) + 3; x < int(r.X+r.W)-3; x++ {
			p := im.RGBAAt(x, y)
			if p.R < 80 && p.G < 80 && p.B < 80 {
				ink++
			}
		}
	}
	if ink < 10 {
		t.Fatal("selected background covers tab text", r, ink)
	}
}

func TestWelcomeTabMovesFormFeedbackAndKeepsSharedFooter(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.agents = agentEditor(setupCatalog())
	f.agents.edit(f.agents.catalog[0])
	f.agents.tab = "常规"
	f.agents.error = "配置错误"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("高级")
	ui.Advance(80 * time.Millisecond)
	saveMid, _ := ui.Find("保存")
	errorMid, _ := ui.Find("配置错误")
	ui.Advance(time.Second)
	saveEnd, _ := ui.Find("保存")
	errorEnd, _ := ui.Find("配置错误")
	if math.Abs(float64(saveMid.X-saveEnd.X)) > 1 || saveMid.W != saveEnd.W || saveMid.H != saveEnd.H || errorMid.X <= errorEnd.X+1 {
		t.Fatal("form should move while the shared footer remains outside the slide", saveMid, saveEnd, errorMid, errorEnd)
	}
}

func TestDialogTabPanelsMoveWholePage(t *testing.T) {
	selected := "a"
	open := true
	ui := n.NewTester(func(c *n.Context) {
		n.DialogBase(c, &open, func(_, panel n.Element) {
			panel.Width(360).Padding(20).Children(func() {
				selected = configurationTabs(c, selected, []configurationTabItem{{ID: "a", Label: "常规"}, {ID: "b", Label: "高级"}}, configurationUnderline, false)
				configurationTabPanel(c.Key("body"), selected, configurationTabIndex([]configurationTabItem{{ID: "a"}, {ID: "b"}}, selected), configurationPanelFit, func() {
					n.Column(c).Height(120).Children(func() {
						n.Box(c).Size(32, 32).Background(n.Hex("#ff0000")).Label("页面首项")
						n.Box(c).Size(32, 32).Background(n.Hex("#00ff00")).Label("页面末项")
					})
				})
			})
		})
	}, 640, 480)
	ui.Advance(time.Second)
	ui.Click("高级")
	ui.Advance(60 * time.Millisecond)
	first, _ := ui.Find("页面首项")
	last, _ := ui.Find("页面末项")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("页面首项")
	if first.X <= end.X+1 || first.X != last.X {
		t.Fatal("dialog reflow erased or split the whole-page translation", first, last, end)
	}
}

func TestAddDeviceTabsKeepSharedFooterOutsideSlide(t *testing.T) {
	m := welcomeModel()
	m.FirstRun = false
	v := New(m, nil, "")
	v.devices = true
	v.sshCheck = sshSelecting
	f := v.setupFlow()
	f.key = ""
	f.step = welcomeAgentOptions
	f.agents = agentEditor(setupCatalog())
	v.sshAgents = f.agents
	v.sshSetup.Info.Minor = 18
	f.agents.edit(f.agents.catalog[0])
	f.agents.tab = "常规"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	for _, tab := range []string{"高级", "常规"} {
		ui.Click(tab)
		if tab == "高级" {
			ui.Advance(60 * time.Millisecond)
		} else {
			ui.Advance(180 * time.Millisecond)
		}
		label := "启动参数"
		if tab == "常规" {
			label = "可执行文件"
		}
		mid, ok := ui.Find(label)
		if !ok {
			t.Fatal(ui.Texts())
		}
		footer, _ := ui.Find("取消")
		ui.Advance(400 * time.Millisecond)
		end, _ := ui.Find(label)
		footerEnd, _ := ui.Find("取消")
		delta := mid.X - end.X
		footerDelta := footer.X - footerEnd.X
		if tab == "常规" {
			delta = mid.X + mid.W - end.X - end.W
			footerDelta = footer.X + footer.W - footerEnd.X - footerEnd.W
		}
		if tab == "高级" && delta <= 1 || tab == "常规" && delta >= -1 || math.Abs(float64(footerDelta)) > 1 || footer.W != footerEnd.W || footer.H != footerEnd.H {
			t.Fatal("add-device form should move without sliding or clipping the shared footer", tab, mid, end, footer, footerEnd)
		}
	}
}

func TestPopoverFollowsAnimatedAnchorWithoutErasingPageMotion(t *testing.T) {
	open, changed := true, false
	ui := n.NewTester(func(c *n.Context) {
		left := float32(20)
		key, index := "a", 0
		if changed {
			left, key, index = 120, "b", 1
		}
		anchor := n.Box(c.Key("anchor")).Absolute().Left(left).Top(20).Size(28, 28).Label("移动锚点").Transition(n.ElementTransition{Position: true, Duration: configurationTabDuration, Ease: n.EaseOut})
		n.PopoverBase(c, anchor, &open, func(panel n.Element) {
			panel.Width(320).Label("跟随浮层").Children(func() {
				configurationTabPanel(c.Key("page"), key, index, configurationPanelFit, func() { n.Box(c).Size(32, 32).Label("浮层内容") })
			})
		})
	}, 640, 480)
	ui.Advance(time.Second)
	changed = true
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	anchor, _ := ui.Find("移动锚点")
	panel, _ := ui.Find("跟随浮层")
	content, _ := ui.Find("浮层内容")
	if math.Abs(float64(panel.X-anchor.X)) > 1 || content.X <= panel.X+1 {
		t.Fatal("popover lost anchor tracking or child translation", anchor, panel, content)
	}
}

func TestWelcomeAccountDisclosureDoesNotClipFooter(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	catalog := setupCatalog()
	catalog[0].Installation.SignInMethods = []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode}
	catalog[0].Account = &domain.RuntimeAccount{State: "authenticated", Label: "Profile", Subscription: "Plus"}
	f.agents = agentEditor(catalog)
	f.agents.edit(catalog[0])
	f.agents.tab = "常规"
	v.deviceState("local").WorkerMinor = 22
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	save, _ := ui.Find("保存")
	cancel, _ := ui.Find("取消")
	for cycle := 0; cycle < 2; cycle++ {
		if err := ui.Click("管理账号"); err != nil {
			t.Fatal(err, ui.Texts())
		}
		for frame := 0; frame < 22; frame++ {
			ui.Advance(time.Second / 60)
			s, _ := ui.Find("保存")
			b, _ := ui.Find("取消")
			if math.Abs(float64(s.W-save.W)) > 1 || math.Abs(float64(s.H-save.H)) > 1 || math.Abs(float64(b.W-cancel.W)) > 1 || math.Abs(float64(b.H-cancel.H)) > 1 {
				t.Fatal("account disclosure clips the shared footer", cycle, frame, save, s, cancel, b)
			}
		}
	}
}

func TestWelcomeAgentConfigurationExitsOppositeIncomingPage(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	f := v.setupFlow()
	f.step = welcomeAgents
	f.agents = agentEditor(setupCatalog())
	ui := n.NewTester(func(c *n.Context) {
		v.frameTime = c.Now()
		theme := *c.Theme()
		theme.Text = n.Hex("#ff0000")
		if f.step == welcomeAgentOptions {
			theme.Text = n.Hex("#0000ff")
		}
		n.Themed(c, &theme).Children(func() { v.welcomeStage(c, &theme, f, false) })
	}, 900, 700)
	ui.Advance(time.Second)
	edge := func(red bool) float64 {
		im := ui.Image()
		edge, count := 900, 0
		if red {
			edge = 0
		}
		for y := 0; y < im.Bounds().Dy(); y++ {
			for x := 0; x < im.Bounds().Dx(); x++ {
				p := im.RGBAAt(x, y)
				if red && p.R > 180 && p.G < 80 && p.B < 80 || !red && p.B > 180 && p.R < 80 && p.G < 80 {
					if red {
						edge = max(edge, x)
					} else {
						edge = min(edge, x)
					}
					count++
				}
			}
		}
		if count == 0 {
			t.Fatal("missing outgoing page pixels", red)
		}
		return float64(edge)
	}
	old := edge(true)
	ui.Click("配置 Custom Agent")
	ui.Advance(60 * time.Millisecond)
	if mid := edge(true); mid >= old-1 {
		t.Fatal("forward navigation sends old page right", old, mid)
	}
	ui.Advance(time.Second)
	old = edge(false)
	ui.Click("取消")
	ui.Advance(60 * time.Millisecond)
	if mid := edge(false); mid <= old+1 {
		t.Fatal("cancel sends old page left", old, mid)
	}
}

func TestConfigurationPanelFirstMountStaysInPlace(t *testing.T) {
	mounted := false
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			if mounted {
				configurationTabPanel(c.Key("new-panel"), "first", 0, configurationPanelFit, func() { n.Text(c, "First mounted page") })
			}
		})
	}, 400, 300)
	ui.Advance(time.Second)
	mounted = true
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	first, ok := ui.Find("First mounted page")
	if !ok {
		t.Fatal("initial page is outside its panel")
	}
	ui.Advance(300 * time.Millisecond)
	settled, _ := ui.Find("First mounted page")
	if first.X != settled.X {
		t.Fatal("first mount plays a tab-switch entrance", first, settled)
	}
}

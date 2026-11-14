package ui

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
)

var settingsSun = svg("M10 1v2M10 17v2M1 10h2M17 10h2M4 4l1 1M15 15l1 1M4 16l1-1M15 5l1-1M13 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0")
var settingsMoon = svg("M17 12A8 8 0 0 1 8 3a7.5 7.5 0 1 0 9 9z")
var settingsInput = svg("M7 3H3v14h4M13 3h4v14h-4M10 2v16M8 2h4M8 18h4")
var settingsKeyboard = svg("M2 4h16v12H2zM5 7h.1M8 7h.1M11 7h.1M14 7h.1M5 10h.1M8 10h.1M11 10h.1M14 10h.1M6 13h8")
var settingsInfo = svg("M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0M10 9v5M10 6h.1")
var settingsBell = svg("M4 14h12l-2-3V7a4 4 0 0 0-8 0v4l-2 3zM8 17h4")

type settingsCategory struct {
	id, title string
	icon      *n.SVG
}

var settingsCategories = []settingsCategory{{"appearance", "外观", settingsSun}, {"input", "消息输入", settingsInput}, {"notifications", "通知", settingsBell}, {"shortcuts", "快捷键", settingsKeyboard}, {"about", "关于", settingsInfo}}

// Settings shares the client model and writer with the conversation window.
// It owns only its own navigation, popup and native-window presentation state.
type Settings struct {
	AuthorizeFullDiskAccess    func(background bool) error
	BackgroundRuntimeAvailable func() bool
	services                   n.Services
	saveError                  string
	owner                      *View
	window                     *mygo.Window
	page, query, error         string
	menus                      View
	fonts                      []n.FontFamily
	fontResults                chan []n.FontFamily
	preview                    markdown.Options
	licenses                   bool
	license                    int
	notificationPending        bool
	notificationMessage        string
}

func NewSettings(owner *View) *Settings {
	s := &Settings{owner: owner.splitRoot(), page: "appearance", license: -1}
	s.owner.settings = s
	return s
}

func (s *Settings) Show() {
	if s.window != nil && !s.window.IsDestroyed() {
		s.window.Show()
		s.window.Focus()
		return
	}
	s.query, s.error = "", ""
	s.window = mygo.NewWindow(mygo.WindowOptions{Title: "Shioji 设置", Width: 968, Height: 756, MinWidth: 760, MinHeight: 600, TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: WindowTrafficLightPosition(), Content: n.View(s.View)})
	s.window.OnClose(func(e *mygo.CloseEvent) {
		if err := s.owner.Flush(); err != nil {
			e.PreventDefault()
			s.error = "无法保存设置：" + err.Error()
			s.window.Invalidate()
		}
	})
	s.window.OnClosed(func() { s.window = nil })
}
func (s *Settings) Close() {
	if s.window != nil {
		s.window.Close()
	}
}

func (s *Settings) OpenAbout() {
	s.selectPage("about")
	s.Show()
	if s.window != nil {
		s.window.Invalidate()
	}
}

func (s *Settings) changed() {
	s.error = ""
	root := s.owner
	root.services.Invalidate()
	s.services.Invalidate()
	if root.Saver != nil {
		data, err := root.Model.EncodeState()
		if err != nil {
			s.error = "无法保存设置：" + err.Error()
		} else {
			root.Saver.Queue(data)
		}
	}
	if source := mygo.ThemeSource(root.Model.Preferences.Appearance); s.window != nil && mygo.Theme.Source() != source {
		mygo.Theme.SetSource(source)
	}
}

func (s *Settings) selectPage(id string) {
	s.page, s.query, s.error = id, "", ""
	s.licenses = false
	s.license = -1
	clear(s.menus.menuOpen)
}

func (s *Settings) View(c *n.Context) {
	s.services = c.Services()
	if s.owner.Saver != nil {
		s.owner.watchStateWriter()
		_, err := s.owner.Saver.Result()
		s.saveError = ""
		if err != nil {
			s.saveError = "无法保存设置：" + err.Error()
		}
	}
	p := &s.owner.Model.Preferences
	*p = p.Normalized()
	c.ReduceMotion(p.ReduceMotion)
	t := *applyAppearance(c, *p)
	if t.Dark {
		t.Background = n.Hex("#151515")
		t.Surface = n.Hex("#1e1e1e")
		t.Border = n.Hex("#353535")
		t.Selection = n.Hex("#2b2b2b")
	} else {
		t.Background = n.Hex("#fafafa")
		t.Surface = n.Hex("#ffffff")
		t.Border = n.Hex("#dedede")
		t.Selection = n.Hex("#dedede")
	}
	c.SetTheme(&t)
	n.Overlay(c, func() {})
	if s.fontResults == nil {
		s.fontResults = make(chan []n.FontFamily, 1)
		services := c.Services()
		go func() { s.fontResults <- n.FontFamilies(); services.Invalidate() }()
	}
	select {
	case s.fonts = <-s.fontResults:
	default:
	}
	navColor := n.Hex("#ededed")
	if t.Dark {
		navColor = n.Hex("#111111")
	}
	n.Row(c).Fill().Background(t.Background).Children(func() {
		n.Column(c.Key("settings-navigation")).Width(184).FillHeight().Background(navColor).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
			n.Column(c.Key("settings-navigation-content")).Fill().Padding(16, 8, 24, 16).Children(func() {
				n.Row(c).FillWidth().Height(32).DragWindow()
				fieldFeedback(c, n.Row(c.Key("settings-search-field")).FillWidth().Height(28).PaddingX(8).Gap(8).AlignItems(n.Center).Radius(6).Background(t.Surface)).Children(func() {
					n.Icon(c, searchIcon).Size(16, 16).TextColor(t.TextMuted)
					n.TextInputBase(c.Key("settings-search"), &s.query).Grow(1).MinWidth(0).Height(24).FontSize(12).FixedLineHeight(18).Label("搜索设置").Placeholder("搜索设置").Changed()
				})
				n.Column(c).FillWidth().Margin(12, 0, 0, 0).Children(func() {
					items := make([]configurationTabItem, 0, len(settingsCategories))
					for _, category := range settingsCategories {
						items = append(items, configurationTabItem{ID: category.id, Label: category.title, Icon: category.icon})
					}
					if next := configurationTabs(c, s.page, items, configurationSidebar, false); next != s.page {
						s.selectPage(next)
					}
				})
				n.Spacer(c).Grow(1)
				version := mygo.App.Version()
				if version == "" {
					version = "开发版"
				}
				n.Text(c, "Shioji "+version).FontSize(11).TextColor(t.TextMuted).PaddingX(8)
			})
		})
		n.Column(c).Grow(1).FillHeight().MinWidth(0).Clip().Children(func() {
			key := s.page
			if strings.TrimSpace(s.query) != "" {
				key = "search"
			}

			buildPage := func() {
				n.Scroll(c.Key("settings-page:" + key)).Fill().Label("设置内容").Children(func() {
					n.Column(c).FillWidth().Padding(configurationSectionGap, configurationInset, configurationSectionGap, configurationInset).Children(func() {
						if key == "search" {
							s.search(c, &t)
							return
						}
						title := ""
						for _, category := range settingsCategories {
							if category.id == s.page {
								title = category.title
							}
						}
						n.Text(c, title).FontSize(configurationTitleSize).FontWeight(400).FixedLineHeight(configurationTitleLine).Margin(0, 0, configurationHeadingGap, 0)
						switch s.page {
						case "appearance":
							s.appearance(c, &t)
						case "input":
							s.input(c, &t)
						case "notifications":
							s.notifications(c, &t)
						case "shortcuts":
							s.shortcuts(c, &t)
						case "about":
							s.about(c, &t)
						}
						if s.saveError != "" {
							n.Text(c, s.saveError).FillWidth().FontSize(12).TextColor(t.Danger).Margin(12, 0, 0, 0)
						}
						if s.error != "" {
							n.Text(c, s.error).FillWidth().FontSize(12).TextColor(t.Danger).Margin(12, 0, 0, 0).Transition(disclosureMotion(c, 180*time.Millisecond))
						}
					})
				})
			}
			if key == "about" {
				page, depth := "about", 0
				if s.licenses {
					page, depth = "licenses", 1
					if s.license >= 0 {
						page, depth = fmt.Sprintf("license:%d", s.license), 2
					}
				}
				configurationTabPanel(c.Key("about-navigation"), page, depth, configurationPanelFill, buildPage, false)
			} else {
				buildPage()
			}
		})
	})
}

func settingsHeading(c *n.Context, t *n.Theme, label string) {
	configurationSectionTitle(c, label).TextColor(t.Text).Margin(0, 0, 8, 0)
}
func settingsGroup(c *n.Context, t *n.Theme, build func()) {
	n.Column(c).FillWidth().Children(build)
}
func settingsRow(c *n.Context, t *n.Theme, label string, build func()) {
	configurationFormRow(c, label, configurationSettingsForm, build)
}

func (s *Settings) picker(c *n.Context, t *n.Theme, label, value string, choices []menuChoice) {
	button := configurationChoiceField(c.Key("picker:"+label), "设置"+label, value, false).Width(160).Background(codeSurface(t)).Radius(6).Border(0, n.Transparent)
	s.menus.choiceMenu(c, t, "settings:"+label, button, choices)
}

func (s *Settings) appearance(c *n.Context, t *n.Theme) {
	p := &s.owner.Model.Preferences
	settingsHeading(c, t, "主题")
	n.Row(c).FillWidth().Gap(12).Children(func() {
		for _, item := range []struct {
			id, label string
			icon      *n.SVG
		}{{"light", "浅色", settingsSun}, {"dark", "深色", settingsMoon}, {"system", "跟随系统", monitor}} {
			selected := p.Appearance == item.id
			button := control(c.Key("theme:"+item.id), 8).Grow(1).MinWidth(0).Height(176).Padding(4).Radius(8).Background(t.Surface).Label("外观"+item.label).Checked(selected).Role(n.RoleRadio).TextColor(t.Text).Border(1, t.Border)
			if selected {
				button.Border(1, t.Text)
			}
			button.Children(func() {
				n.Column(c).FillWidth().Gap(8).Children(func() {
					n.Column(c).FillWidth().Height(132).Radius(4).Clip().Draw(func(paint *n.Painter, r n.Rect) { drawThemeSample(paint, r, item.id) })
					n.Row(c).FillWidth().PaddingX(4).AlignItems(n.Baseline).Children(func() {
						inlineIconText(c, item.icon, item.label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 6, t.Text).Grow(1)
						if selected {
							inlineSymbol(c, check, item.label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.Text)
						}
					})
				})
			})
			if button.Clicked() {
				p.Appearance = item.id
				s.changed()
			}
		}
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "文字")
		settingsGroup(c, t, func() {
			settingsRow(c, t, "界面字体", func() { s.fontPicker(c, t, false) })
			settingsRow(c, t, "回复字号", func() {
				choices := []menuChoice{}
				for size := 12; size <= 20; size++ {
					choices = append(choices, menuChoice{id: fmt.Sprint(size), name: fmt.Sprintf("%d px", size), checked: p.ReplySize == size, choose: func() { p.ReplySize = size; s.changed() }})
				}
				s.picker(c, t, "回复字号", fmt.Sprintf("%d px", p.ReplySize), choices)
			})
			settingsRow(c, t, "代码字体", func() { s.fontPicker(c, t, true) })
		})
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "动态效果")
		settingsGroup(c, t, func() {
			settingsRow(c, t, "减少动态效果", func() {
				value := "跟随系统"
				if p.ReduceMotion {
					value = "开启"
				}
				s.picker(c, t, "减少动态效果", value, []menuChoice{{id: "system", name: "跟随系统", checked: !p.ReduceMotion, choose: func() { p.ReduceMotion = false; s.changed() }}, {id: "reduce", name: "开启", checked: p.ReduceMotion, choose: func() { p.ReduceMotion = true; s.changed() }}})
			})
		})
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Radius(8).Background(t.Surface).Border(1, t.Border).Clip().Children(func() {
		n.Row(c).Height(32).PaddingX(14).FillWidth().AlignItems(n.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			n.Text(c, "对话预览").FontSize(11).TextColor(t.TextMuted).Grow(1)
			n.Text(c, fmt.Sprintf("%d px · %s 12 px", p.ReplySize, p.CodeFont)).FontSize(11).TextColor(t.TextMuted)
		})
		s.preview.Theme = replyTheme(*p)
		n.Column(c).FillWidth().Padding(14, 16, 16, 16).Children(func() {
			markdown.View(c, "旋转只改变局部结构，节点的大小顺序保持不变。\n\n```go\nfunc rotateLeft(root *Node) *Node { /* … */ }\n```", &s.preview).FillWidth()
		})
	})
}

func drawThemeSample(p *n.Painter, r n.Rect, kind string) {
	light, dark := n.Hex("#ffffff"), n.Hex("#191919")
	bg := light
	if kind == "dark" {
		bg = dark
	}
	p.Fill(r, bg, 4)
	if kind == "system" {
		half := r
		half.X += r.W / 2
		half.W = r.W / 2
		p.Fill(half, dark, 0)
	}
	nav := r
	nav.W = r.W * .25
	navColor := n.Hex("#ededed")
	if kind == "dark" {
		navColor = n.Hex("#111111")
	}
	p.Fill(nav, navColor, 0)
	for i := 0; i < 3; i++ {
		p.Fill(n.Rect{X: r.X + 7, Y: r.Y + 12 + float32(i)*10, W: max(8, nav.W-14), H: 4}, n.Hex("#999999"), 2)
	}
	x := r.X + nav.W + 12
	ink := n.Hex("#777777")
	if kind == "dark" {
		ink = n.Hex("#bdbdbd")
	}
	for i := 0; i < 3; i++ {
		width := (r.W - nav.W - 24) * []float32{.62, 1, .78}[i]
		p.Fill(n.Rect{X: x, Y: r.Y + 14 + float32(i)*11, W: max(4, width), H: 3}, ink, 1.5)
	}
	box := n.Rect{X: x, Y: r.Y + r.H - 26, W: max(4, r.W-nav.W-24), H: 16}
	fill := n.Hex("#eeeeee")
	if kind == "dark" {
		fill = n.Hex("#2b2b2b")
	}
	p.Fill(box, fill, 3)
	if kind == "system" {
		right := box.X + box.W
		box.X = max(box.X, r.X+r.W/2)
		box.W = max(0, right-box.X)
		p.Fill(box, n.Hex("#2b2b2b"), 0)
	}
}

func (s *Settings) fontPicker(c *n.Context, t *n.Theme, code bool) {
	p := &s.owner.Model.Preferences
	label, value := "界面字体", p.InterfaceFont
	if code {
		label, value = "代码字体", p.CodeFont
	}
	choices := []menuChoice{}
	add := func(id, name string) {
		choices = append(choices, menuChoice{id: id, name: name, checked: value == id, choose: func() {
			if code {
				p.CodeFont = id
			} else {
				p.InterfaceFont = id
			}
			s.changed()
		}})
	}
	if !code {
		add("system-ui", "系统字体")
	} else {
		add("SF Mono", "SF Mono")
	}
	for _, font := range s.fonts {
		if font.Name == "SF Mono" && code || font.Name == "system-ui" {
			continue
		}
		if !code || font.Monospaced {
			add(font.Name, font.Name)
		}
	}
	if value == "system-ui" {
		value = "系统字体"
	}
	s.picker(c, t, label, value, choices)
}

func bindingLabel(binding string) string {
	return strings.NewReplacer("Cmd+", "⌘ ", "Shift+", "⇧ ", "Ctrl+", "⌃ ", "Alt+", "⌥ ", "Enter", "↩").Replace(binding)
}

func (s *Settings) input(c *n.Context, t *n.Theme) {
	p := &s.owner.Model.Preferences
	n.Row(c).FillWidth().Height(configurationSectionHeader).AlignItems(n.Start).Children(func() {
		n.Text(c, "按键").FontSize(configurationSectionSize).FontWeight(500).FixedLineHeight(configurationTitleLine).Grow(1)
		if configurationAction(c, "恢复默认按键").MarginEnd(-8).Clicked() {
			p.Shortcuts = app.DefaultShortcuts()
			s.changed()
		}
	})
	settingsGroup(c, t, func() {
		for _, item := range []struct{ id, label, value string }{{"send", "发送或加入队列", p.Shortcuts.Send}, {"newline", "换行", p.Shortcuts.Newline}, {"steer", "立即介入当前轮", p.Shortcuts.Steer}} {
			settingsRow(c, t, item.label, func() {
				button := control(c.Key(item.id)).Width(160).Height(28).PaddingX(0).Justify(n.End).Label("修改" + item.label + "按键").Children(func() {
					n.Text(c, bindingLabel(item.value)).FontSize(12).FixedLineHeight(20).PaddingX(7).Background(codeSurface(t)).Border(1, t.Border).Radius(5)
				})
				choices := []menuChoice{}
				for _, binding := range app.ComposerBindings {
					choices = append(choices, menuChoice{id: binding, name: bindingLabel(binding), checked: item.value == binding, choose: func() {
						if err := p.SetShortcut(item.id, binding); err != nil {
							s.error = err.Error()
							return
						}
						s.changed()
					}})
				}
				s.menus.choiceMenu(c, t, "binding:"+item.id, button, choices)
			})
		}
	})
	n.Text(c, "同一按键组合只能分配给一个动作").FontSize(11).TextColor(t.TextMuted).Margin(12, 0, 0, 0)
}

func (s *Settings) shortcuts(c *n.Context, t *n.Theme) {
	settingsHeading(c, t, "会话导航")
	settingsGroup(c, t, func() {
		for _, item := range []struct{ label, key string }{{"新建会话", "⌘ N"}, {"搜索会话", "⌘ K"}, {"下一个待处理", "⌘ ⇧ J"}, {"返回上一个会话", "⌘ ⇧ H"}} {
			settingsRow(c, t, item.label, func() {
				n.Text(c, item.key).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).Padding(2, 8).Background(codeSurface(t)).Border(1, t.Border).Radius(5)
			})
		}
	})
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsHeading(c, t, "消息")
		settingsGroup(c, t, func() {
			button := configurationRowControl(c, 6).Height(configurationRowHeight).Label("前往消息输入").Children(func() {
				settingsLinkContent(c, t, "发送、换行与立即介入", "消息输入")
			})
			n.Column(c).FillWidth().Height(.5).Background(t.Border)
			if button.Clicked() {
				s.selectPage("input")
			}
		})
	})
}

func settingsLinkContent(c *n.Context, t *n.Theme, label, action string) {
	font := n.Font{Family: t.Font, Size: 13, Weight: 400}
	n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset(label, font))).Children(func() {
		n.Text(c, label).FontSize(13).FixedLineHeight(20).Grow(1)
		n.Text(c, action).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
		inlineSymbol(c, disclosureRight, action, n.Font{Family: t.Font, Size: 12, Weight: 400}, 13, t.TextMuted)
	})
}

func (s *Settings) about(c *n.Context, t *n.Theme) {
	if s.licenses {
		s.licenseView(c, t)
		return
	}
	n.Text(c, "Shioji").FontSize(32).FontWeight(500).FixedLineHeight(40).Margin(8, 0, 0, 0)
	n.Text(c, "潮路 / しおじ").FontSize(13).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted).Margin(4, 0, 0, 0)
	version := mygo.App.Version()
	if version == "" {
		version = "开发版"
	}
	n.Text(c, "版本 "+version).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted).Margin(16, 0, 0, 0)
	n.Column(c).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		settingsGroup(c, t, func() {
			platform := "macOS · Apple Silicon"
			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				platform = runtime.GOOS + " · " + runtime.GOARCH
			}
			settingsRow(c, t, "运行平台", func() { n.Text(c, platform).FontSize(12).TextColor(t.TextMuted) })
			button := configurationRowControl(c, 6).Height(configurationRowHeight).Label("查看第三方许可").Children(func() {
				settingsLinkContent(c, t, "第三方许可", "查看")
			})
			n.Column(c).FillWidth().Height(.5).Background(t.Border)
			if button.Clicked() {
				s.licenses = true
			}
		})
	})
	n.Column(c.Key("full-disk-access")).FillWidth().Margin(configurationSectionGap, 0, 0, 0).Children(func() {
		s.fullDiskAccessSetting(c, t)
	})
}

func (s *Settings) search(c *n.Context, t *n.Theme) {
	n.Text(c, "搜索设置").FontSize(configurationTitleSize).FontWeight(400).FixedLineHeight(configurationTitleLine).Margin(0, 0, configurationHeadingGap, 0)
	query := strings.ToLower(strings.TrimSpace(s.query))
	found := false
	for _, entry := range []struct{ page, label string }{{"appearance", "主题"}, {"appearance", "界面字体"}, {"appearance", "回复字号"}, {"appearance", "代码字体"}, {"appearance", "减少动态效果"}, {"input", "发送或加入队列"}, {"input", "换行"}, {"input", "立即介入当前轮"}, {"notifications", "通知"}, {"notifications", "回复完成"}, {"notifications", "需要回答或授权"}, {"notifications", "执行失败"}, {"notifications", "提示音"}, {"notifications", "前台也通知"}, {"shortcuts", "新建会话"}, {"shortcuts", "搜索会话"}, {"shortcuts", "下一个待处理"}, {"shortcuts", "返回上一个会话"}, {"about", "版本"}, {"about", "第三方许可"}, {"about", "授予完全磁盘访问权限"}} {
		category := settingsCategories[slices.IndexFunc(settingsCategories, func(x settingsCategory) bool { return x.id == entry.page })]
		if !strings.Contains(strings.ToLower(entry.label+" "+category.title), query) {
			continue
		}
		found = true
		label := category.title + " · " + entry.label
		if control(c.Key(label)).FillWidth().MarginX(-8).Height(configurationRowHeight).PaddingX(8).Label(label).Children(func() {
			inlineIconText(c, category.icon, label, n.Font{Family: t.Font, Size: configurationLabelSize, Weight: 400}, 16, 8, t.Text)
		}).Clicked() {
			s.selectPage(entry.page)
		}
	}
	if !found {
		n.Text(c, "没有匹配的设置").FontSize(13).TextColor(t.TextMuted)
	}
}

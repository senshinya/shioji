package ui

import (
	_ "embed"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"net/url"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"strings"
	"time"
)

// Official nine-dot service mark; provenance lives with the approved design.
//
//go:embed assets/tailscale-mark.svg
var tailscaleMarkSVG []byte
var tailscaleMark = n.MustParseSVG(tailscaleMarkSVG)

type tailnetUI struct {
	canceledID                         string
	hostname, query, selected, lastURL string
	joinID                             string
	joining, autoOpen                  bool
	peer, refreshID                    string
	refreshUntil                       time.Time
	logout                             bool
}

func (v *View) networkUI() *tailnetUI {
	if v.tailnet == nil {
		v.tailnet = &tailnetUI{hostname: "shioji-mac"}
	}
	return v.tailnet
}
func (v *View) tailnetAction(kind string) {
	u := v.networkUI()
	if kind == "join" {
		u.joining = true
		u.autoOpen = true
		u.lastURL = ""
		u.joinID = domain.NewID()
	}
	if kind == "cancel" || kind == "pause" || kind == "logout" {
		u.canceledID = u.joinID
		u.joining = false
		u.autoOpen = false
		u.lastURL = ""
	}
	if v.Controller != nil {
		v.Controller.Do(app.Job{Method: "$tailnet", Input: tailnet.Action{Kind: kind, Hostname: strings.TrimSpace(u.hostname), ID: func() string {
			if kind == "join" {
				return u.joinID
			}
			return domain.NewID()
		}()}})
	}
}
func (v *View) beginTailnet() {
	v.saveWelcomeSSHInput()
	v.cancelSSHCheck()
	v.sshCheck = sshEditing
	v.sshError = ""
	v.sshTailnetPeer = ""
	f := v.setupFlow()
	f.key = ""
	f.route = welcomeRouteTailnet
	u := v.networkUI()
	if v.Model.Tailnet.Authorizing && v.Model.Tailnet.OperationID != u.canceledID {
		u.joining = true
		u.joinID = v.Model.Tailnet.OperationID
	}
	if v.Model.Tailnet.State == "Running" && !u.joining {
		v.welcomeStep(welcomeTailnetPeers, 1)
	} else {
		v.welcomeStep(welcomeTailnetJoin, 1)
	}
}
func (v *View) syncTailnet() {
	u := v.tailnet
	if u == nil {
		return
	}
	s := v.Model.Tailnet
	if u.autoOpen && u.authorizationURL(s) != "" && s.AuthURL != u.lastURL {
		if uri, err := url.Parse(s.AuthURL); err == nil && uri.Scheme == "https" && uri.Hostname() != "" && uri.User == nil {
			u.lastURL = s.AuthURL
			if err = openAuthorizationURL(u.authorizationURL(s)); err != nil {
				v.sshError = "无法打开授权页面，请重新打开浏览器"
			}
		}
	}
	if s.Error != "" && !s.Authorizing && (!u.joining || s.OperationID == u.joinID) {
		u.joining = false
	}
	if s.State == "Running" && (!u.joining || s.OperationID == u.joinID && !s.Authorizing) {
		u.autoOpen = false
		wasJoining := u.joining
		u.joining = false
		if v.welcome != nil && v.welcome.step == welcomeTailnetJoin && wasJoining {
			v.welcomeStep(welcomeTailnetPeers, 1)
		}
	}
}
func (v *View) setupSSH(port int) transport.SSH {
	s := transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port, TailnetPeer: v.sshTailnetPeer}
	if v.Controller != nil && s.TailnetPeer != "" {
		if routed, err := v.Controller.TailnetSSH(s); err == nil {
			s = routed
		}
	}
	return s
}
func tailnetValue(c *n.Context, label, text string) {
	setupRow(c, label, func() {
		n.Row(c).FillWidth().Height(28).AlignItems(n.Center).Children(func() {
			n.Text(c, text).FontSize(12).FixedLineHeight(20).Transform(n.Translation(0, inlineCenterOffset(text, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}))).SingleLine().Ellipsis("…").Tooltip(text)
		})
	})
}
func (v *View) tailnetJoinBody(c *n.Context, t *n.Theme) {
	u := v.networkUI()
	s := v.Model.Tailnet
	pending := u.pending(s)
	tailnetValue(c, "网络服务", "Tailscale")
	setupRow(c, "Tailnet 名称", func() { setupField(c, "Tailnet 节点名称", &u.hostname, "shioji-mac", pending) })
	n.Row(c.Key("授权方式")).FillWidth().MinHeight(configurationRowHeight).PaddingY(6).Gap(configurationSectionGap).AlignItems(n.Baseline).Children(func() {
		n.Text(c, "授权方式").FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).Grow(1)
		n.Row(c.Key("authorization-status")).Width(320).Height(configurationControlHeight).PaddingY(4).Gap(8).AlignItems(n.Baseline).Children(func() {
			label := "浏览器授权"
			if pending {
				label = "正在连接网络"
				if u.authorizationURL(s) != "" {
					label = "等待浏览器授权"
				}
				if s.State == "NeedsMachineAuth" {
					label = "等待网络管理员批准"
				}
				serviceSpinner(c.Key("spinner"), label, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
			}
			n.Text(c.Key("caption"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		})
	})
	v.welcomeError(c, s.Error)
}
func (v *View) tailnetJoinPage(c *n.Context, t *n.Theme) {
	v.tailnetHeading(c, t, "连接 Tailnet", !v.Model.FirstRun)
	v.tailnetJoinBody(c, t)
	u := v.networkUI()
	waiting := u.pending(v.Model.Tailnet)
	label := "加入网络"
	if waiting {
		label = "重新打开浏览器"
	}
	back := "返回"
	if waiting {
		back = "取消授权"
	}
	v.welcomeFooter(c, back, label, v.Controller == nil || strings.TrimSpace(u.hostname) == "" || waiting && u.authorizationURL(v.Model.Tailnet) == "", func() {
		if waiting {
			v.tailnetAction("cancel")
		} else if v.Model.FirstRun {
			v.welcomeStep(welcomeDevice, -1)
		} else {
			v.dismissDeviceSetup()
		}
	}, func() {
		if waiting {
			if err := openAuthorizationURL(u.authorizationURL(v.Model.Tailnet)); err != nil {
				v.sshError = "无法打开授权页面"
			}
		} else {
			v.tailnetAction("join")
		}
	})
}
func (v *View) tailnetPeerPage(c *n.Context, t *n.Theme) {
	u := v.networkUI()
	s := v.Model.Tailnet
	v.tailnetHeading(c, t, "选择网络设备", !v.Model.FirstRun)
	n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Baseline).Margin(0, 0, 12).Children(func() {
		inlineIconText(c, tailscaleMark, s.Network, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 8, t.TextMuted)
		n.Spacer(c).Grow(1)
		if configurationAction(c, "更换网络").MarginEnd(-8).Clicked() {
			v.welcomeStep(welcomeTailnetJoin, -1)
		}
	})
	textField(c.Key("peer-search"), &u.query).FillWidth().Height(32).Padding(6, 10).FontSize(12).Label("搜索 Tailnet 设备").Placeholder("搜索设备名称或地址")
	n.Scroll(c.Key("peers")).Width(v.setupFlow().width+16).MarginX(-8).PaddingX(8).MaxHeight(196).Margin(12, 0, 0).Label("Tailnet 设备列表").Children(func() {
		count := 0
		for _, p := range s.Peers {
			name := v.tailnetPeerName(p)
			if q := strings.ToLower(strings.TrimSpace(u.query)); q != "" && !strings.Contains(strings.ToLower(name+" "+p.Name+" "+p.DNSName+" "+p.Address), q) {
				continue
			}
			count++
			supported := p.OS == "macOS"
			row := control(c.Key(p.ID), 6).Width(v.setupFlow().width + 16).MarginX(-8).Height(56).Padding(8).Label("选择 Tailnet 设备：" + name).Disabled(!p.Online || !supported)
			if u.selected == p.ID {
				row.Background(t.SurfacePressed)
			}
			row.Children(func() {
				n.Row(c).FillWidth().Gap(10).AlignItems(n.Baseline).Children(func() {
					inlineSymbol(c, v.tailnetPeerGlyph(p), name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 16, t.TextMuted)
					n.Column(c).Grow(1).MinWidth(0).Children(func() {
						n.Text(c, name).FontSize(13).FixedLineHeight(20).SingleLine()
						n.Text(c, p.Address).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).SingleLine()
					})
					status := "在线"
					if !p.Online {
						status = "离线"
					}
					if !supported {
						status = "不支持的系统"
					}
					n.Text(c, status).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
					inlineSymbol(c, check, status, n.Font{Family: t.Font, Size: 11, Weight: 400}, 14, t.TextMuted).Opacity(func() float32 {
						if u.selected == p.ID {
							return 1
						}
						return 0
					}())
				})
			})
			if row.Clicked() {
				u.selected = p.ID
			}
		}
		if count == 0 {
			n.Text(c, "没有匹配的设备").PaddingY(20).FontSize(12).TextColor(t.TextMuted)
		}
	})
	var chosen *tailnet.Peer
	for _, p := range s.Peers {
		if p.ID == u.selected && p.Online && p.OS == "macOS" {
			copy := p
			chosen = &copy
		}
	}
	v.welcomeError(c, s.Error)
	next := "连接设备"
	if v.Model.FirstRun {
		next = "继续"
	}
	v.welcomeFooter(c, "返回", next, chosen == nil || s.State != "Running", func() {
		if v.Model.FirstRun {
			v.welcomeStep(welcomeDevice, -1)
		} else {
			v.dismissDeviceSetup()
		}
	}, func() {
		v.saveWelcomeSSHInput()
		v.sshTailnetPeer = chosen.ID
		f := v.setupFlow()
		draft, ok := f.tailnetSSH[chosen.ID]
		if !ok {
			draft = welcomeSSHInput{target: chosen.Address, name: v.tailnetPeerName(*chosen)}
		}
		f.icon = draft.icon
		v.sshTarget, v.sshName, v.sshPort, f.sshOptions = draft.target, draft.name, draft.port, draft.options
		v.sshError = ""
		v.sshCheck = sshEditing
		v.welcomeStep(welcomeSSH, 1)
	})
}
func (v *View) tailnetPage(c *n.Context, t *n.Theme) {
	s := v.Model.Tailnet
	u := v.networkUI()
	joining := s.State == "NotStarted" || s.State == "NeedsLogin" || s.State == "" || u.joining
	n.Column(c).Fill().Children(func() {
		width, _ := c.Size()
		header := n.Column(c).FillWidth().Height(88).Padding(28, configurationInset, 20)
		if width < 900 {
			header.Padding(8, configurationInset, 6)
		}
		header.Children(func() {
			if width < 900 {
				if configurationAction(c, "返回设备列表").MarginStart(64).Clicked() {
					v.deviceList = true
				}
			}
			n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Text(c, "Tailnet").FontSize(configurationTitleSize).FixedLineHeight(configurationTitleLine).Grow(1)
				if !joining {
					trigger := iconButton(c, moreActionsIcon, "Tailnet 网络操作")
					v.choiceMenu(c, t, "tailnet-actions", trigger, []menuChoice{{id: "logout", name: "退出网络…", choose: func() { u.logout = true }}})
				}
			})
		})
		n.Scroll(c).FillWidth().Grow(1).Padding(20, configurationInset, 24).Children(func() {
			content := n.Column(c).FillWidth().Label("Tailnet 管理内容")
			content.Children(func() {
				if joining {
					n.Column(c).Width(560).MaxWidth(560).AlignSelf(n.Center).Label("Tailnet 连接表单").Children(func() {
						n.Row(c).FillWidth().MinHeight(28).Margin(0, 0, 28).Gap(12).AlignItems(n.Baseline).Children(func() {
							inlineSymbol(c, tailscaleMark, "连接 Tailnet", n.Font{Family: t.Font, Size: 16, Weight: 500}, 28, t.TextMuted)
							n.Text(c, "连接 Tailnet").FontSize(16).FontWeight(500).FixedLineHeight(24)
						})
						v.tailnetJoinBody(c, t)
						label := "加入网络"
						waiting := u.pending(s)
						if waiting {
							label = "重新打开浏览器"
						}
						n.Row(c).FillWidth().Gap(8).Justify(n.End).Margin(configurationSectionGap, 0, 0).Children(func() {
							if waiting && configurationAction(c, "取消授权").Clicked() {
								v.tailnetAction("cancel")
							}
							if n.PrimaryButton(c, label).Height(configurationControlHeight).PaddingX(12).Radius(6).FontSize(configurationValueSize).Disabled(v.Controller == nil || strings.TrimSpace(u.hostname) == "" || waiting && u.authorizationURL(s) == "").Clicked() {
								if waiting {
									if err := openAuthorizationURL(u.authorizationURL(s)); err != nil {
										v.sshError = "无法打开授权页面"
									}
								} else {
									v.tailnetAction("join")
								}
							}
						})
					})
					return
				}
				n.Row(c.Key("node")).FillWidth().MinHeight(48).Gap(14).AlignItems(n.Start).Children(func() {
					n.Icon(c, tailscaleMark).Size(28, 28).TextColor(t.TextMuted)
					n.Column(c).Grow(1).MinWidth(0).Children(func() {
						n.Row(c).FillWidth().Height(28).Gap(14).AlignItems(n.Center).Children(func() {
							font := n.Font{Family: t.Font, Size: 16, Weight: 500}
							name := v.Model.LocalName()
							n.Text(c, name).Grow(1).MinWidth(0).FontSize(16).FontWeight(500).FixedLineHeight(24).SingleLine().Ellipsis("…").Tooltip(name).Transform(n.Translation(0, inlineCenterOffset(name, font)))
							v.tailnetConnectionStatus(c.Key("status"), t, s)
							on := s.State != "Stopped"
							if configurationSwitch(c, &on).Label("Tailnet 连接开关").Disabled(v.Controller == nil).Changed() {
								if on {
									v.tailnetAction("resume")
								} else {
									v.tailnetAction("pause")
								}
							}
						})
						n.Text(c, s.Network).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Tooltip(s.Network)
					})
				})
				n.Column(c).FillWidth().Margin(22, 0, 0).Children(func() {
					deviceMeta(c, "Tailnet 名称", s.Hostname)
					deviceConfigRow(c, "Tailnet 地址", func() {
						n.Row(c).FillWidth().Gap(7).Justify(n.End).AlignItems(n.Center).Children(func() {
							address := s.Address
							if address == "" {
								address = "尚未分配"
							}
							n.Text(c, address).FontSize(12).FixedLineHeight(20).Selectable().Transform(n.Translation(0, inlineCenterOffset(address, n.Font{Family: t.Font, Size: 12, Weight: 400})))
							tailnetCopyButton(c, s.Address, "复制本机 Tailnet 地址")
						})
					})
					deviceConfigRow(c, "入站转发", func() {
						on := s.InboundEnabled
						n.Row(c).FillWidth().Justify(n.End).Children(func() {
							if configurationSwitch(c, &on).Label("入站转发开关").Tooltip("转发到本机相同 TCP 端口").Disabled(v.Controller == nil).Changed() {
								if on {
									v.tailnetAction("incoming-on")
								} else {
									v.tailnetAction("incoming-off")
								}
							}
						})
					})
				})
				v.welcomeError(c, s.Error)
				if s.State != "Running" && s.State != "Stopped" {
					if configurationAction(c, "重新连接").MarginStart(-8).Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(v.Controller == nil || s.State == "Starting").Clicked() {
						v.tailnetAction("resume")
					}
				}
				v.tailnetNetworkPeers(c.Key("network-peers"), t, s)
			})
		})
	})
	v.tailnetLogoutDialog(c.Key("tailnet-logout"), t)
}

func tailnetCopyButton(c *n.Context, value, label string) {
	button := control(c.Key(label)).Size(28, 28).Label(label).Disabled(value == "")
	until := n.Local(button, "copied-until", func() time.Time { return time.Time{} })
	if button.Clicked() {
		c.WriteClipboard(value)
		*until = c.Now().Add(2 * time.Second)
	}
	icon, tip := copyReplyIcon, label
	if c.Now().Before(*until) {
		icon, tip = check, "已复制"
		c.After(until.Sub(c.Now()))
	}
	button.Tooltip(tip).Children(func() {
		n.Icon(c.Key(tip), icon).Size(16, 16).TextColor(c.Theme().TextMuted).Label(tip).Transition(fadeMotion(c, 120*time.Millisecond))
	})
}

func (v *View) tailnetConnectionStatus(c *n.Context, t *n.Theme, s tailnet.Snapshot) {
	label := tailnetStatus(s)
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	n.Row(c).Gap(7).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset(label, font))).Children(func() {
		if s.State == "Starting" || s.State == "NeedsMachineAuth" {
			serviceSpinner(c.Key("spinner"), label, font, t.TextMuted)
		} else {
			color := t.TextMuted
			if s.State == "Running" {
				color = t.Success
			} else if s.Error != "" {
				color = t.Danger
			}
			statusDot(c, label, font, color, false)
		}
		n.Text(c.Key("caption"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
	})
}

func (v *View) tailnetLogoutDialog(c *n.Context, t *n.Theme) {
	u := v.networkUI()
	dialog := n.DialogBase(c, &u.logout, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .16))
		panel.Width(360).Padding(configurationDialogInset).Gap(12).Radius(12).Background(raised(t)).Border(1, t.Border).Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, .16)).Transition(popupMotion(c)).Children(func() {
			n.Text(c, "退出 Tailnet？").FontSize(14).FontWeight(500).FixedLineHeight(20)
			n.Text(c, "退出后，这台 Mac 将断开 Tailnet。直连 SSH 不受影响。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			n.Row(c).FillWidth().Justify(n.End).Gap(8).Margin(8, 0, 0).Children(func() {
				if configurationAction(c, "取消").Label("取消退出网络").Clicked() {
					u.logout = false
				}
				if n.PrimaryButton(c, "退出网络").Height(28).PaddingX(12).Radius(6).FontSize(12).Label("确认退出网络").Disabled(v.Controller == nil).Clicked() {
					u.logout = false
					v.tailnetAction("logout")
				}
			})
		})
	})
	if dialog.Dismissed() {
		u.logout = false
	}
}

func (v *View) tailnetNetworkPeers(c *n.Context, t *n.Theme, s tailnet.Snapshot) {
	u := v.networkUI()
	if u.refreshID == s.OperationID || s.State != "Running" || s.Error != "" || !c.Now().Before(u.refreshUntil) {
		u.refreshID = ""
	}
	refreshing := u.refreshID != "" && u.refreshID != s.OperationID && c.Now().Before(u.refreshUntil) && s.State == "Running" && s.Error == ""
	if refreshing {
		c.After(u.refreshUntil.Sub(c.Now()))
	}
	n.Column(c).FillWidth().Margin(34, 0, 0).Children(func() {
		deviceDivider(c)
		n.Row(c).FillWidth().Height(32).Margin(20, 0, 8).Gap(8).AlignItems(n.Center).Children(func() {
			n.Row(c).Grow(1).Gap(6).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset("网络设备", n.Font{Family: t.Font, Size: 14, Weight: 500}))).Children(func() {
				n.Text(c, "网络设备").FontSize(14).FontWeight(500).FixedLineHeight(20)
				if s.State == "Running" || len(s.Peers) > 0 {
					n.Text(c, fmt.Sprint(len(s.Peers))).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				}
			})
			button := control(c.Key("refresh")).Size(28, 28).Label("刷新网络设备").Tooltip("刷新网络设备").Disabled(v.Controller == nil || s.State != "Running" || refreshing)
			button.Children(func() {
				if refreshing {
					serviceSpinner(c.Key("spinner"), "刷新", n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
				} else {
					n.Icon(c, refreshIcon).Size(16, 16).TextColor(t.TextMuted)
				}
			})
			if button.Clicked() {
				id := domain.NewID()
				if v.Controller.Do(app.Job{Method: "$tailnet", Input: tailnet.Action{Kind: "refresh", ID: id}}) {
					u.refreshID, u.refreshUntil = id, c.Now().Add(10*time.Second)
				}
			}
		})
		if s.State == "Stopped" || s.State == "Starting" || len(s.Peers) == 0 {
			label := "没有可见的网络设备"
			if s.State == "Stopped" {
				label = "连接网络后显示设备"
			} else if s.State == "Starting" {
				label = "正在获取网络设备"
			}
			n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(18).Transition(fadeMotion(c, 160*time.Millisecond))
			return
		}
		for _, p := range s.Peers {
			name := v.tailnetPeerName(p)
			if s.State != "Running" {
				p.Online = false
			}
			n.Column(c.Key(p.ID)).FillWidth().Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
				row := configurationRowControl(c.Key("peer"), 6).MinHeight(64).PaddingY(12).Label("查看 Tailnet 设备：" + name)
				row.Children(func() {
					n.Row(c).FillWidth().Gap(10).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: t.Font, Size: 13, Weight: 400}
						inlineSymbol(c, v.tailnetPeerGlyph(p), name, font, 16, t.TextMuted)
						n.Column(c).Grow(1).MinWidth(0).Children(func() {
							n.Text(c, name).FontSize(13).FixedLineHeight(20).SingleLine().Ellipsis("…").Tooltip(name)
							n.Text(c, p.Address).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine()
						})
						label, color := "离线", t.TextMuted
						if p.Online {
							label, color = "在线", t.Success
						}
						if s.State != "Running" {
							label = "状态未知"
						}
						n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
							statusDot(c, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, color, false)
							n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
							n.Text(c, "· "+tailnetLatency(p, c.Now())).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
						})
						angle := float32(-90)
						if u.peer == p.ID {
							angle = 0
						}
						animation := motionAnimation(c, 260*time.Millisecond)
						if c.Preferences().ReduceMotion {
							animation.Duration = 0
						}
						inlineSymbolWithAnimation(c, chevron, name, font, 14, t.TextMuted, animation, angle).MarginStart(12)
					})
				})
				if row.Clicked() {
					if u.peer == p.ID {
						u.peer = ""
					} else {
						u.peer = p.ID
					}
				}
				frame := n.Column(c.Key("detail")).FillWidth().Clip()
				animatedContentHeight(c, frame, u.peer == p.ID, 260*time.Millisecond, func() float32 {
					n.Column(c).FillWidth().Padding(0, 0, 12, 26).Children(func() {
						tailnetPeerDetail(c, "Tailnet 地址", func() {
							n.Row(c).Gap(7).AlignItems(n.Center).Children(func() {
								n.Text(c, p.Address).FontSize(12).FixedLineHeight(20).Selectable().Transform(n.Translation(0, inlineCenterOffset(p.Address, n.Font{Family: t.Font, Size: 12, Weight: 400})))
								tailnetCopyButton(c, p.Address, "复制 Tailnet 设备地址："+p.Name)
							})
						})
						for _, meta := range [][2]string{{"Tailnet 名称", p.Name}, {"连接路径", tailnetPathLabel(p, c.Now())}, {"往返延迟", tailnetLatency(p, c.Now())}, {"系统", p.OS}} {
							tailnetPeerDetail(c, meta[0], func() {
								n.Text(c, meta[1]).FontSize(12).FixedLineHeight(20).Selectable().Transform(n.Translation(0, inlineCenterOffset(meta[1], n.Font{Family: t.Font, Size: 12, Weight: 400})))
							})
						}
					})
					return 0
				})
				deviceDivider(c)
			})
		}
	})
}

func tailnetPeerDetail(c *n.Context, label string, value func()) {
	n.Row(c.Key(label)).FillWidth().MinHeight(28).Gap(16).AlignItems(n.Center).Children(func() {
		n.Text(c, label).Width(94).FontSize(12).FixedLineHeight(20).TextColor(c.Theme().TextMuted).Transform(n.Translation(0, inlineCenterOffset(label, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400})))
		value()
	})
}

func tailnetPathLabel(p tailnet.Peer, now time.Time) string {
	path := "尚未确认"
	if m := p.Measurement; m != nil {
		switch m.Path {
		case "direct":
			path = "直接连接"
		case "derp":
			path = "DERP 中继 · " + m.RegionName + " (" + m.Region + ")"
		case "peer-relay":
			path = "设备中继"
		}
		if !p.Online || now.Sub(m.ObservedAt) > 30*time.Second {
			path = "上次 · " + path
		}
	}
	return path
}

func tailnetStatus(s tailnet.Snapshot) string {
	switch s.State {
	case "Running":
		return "已连接"
	case "NeedsLogin":
		return "等待授权"
	case "NeedsMachineAuth":
		return "等待网络管理员批准"
	case "Stopped":
		return "已暂停"
	case "Starting":
		return "正在连接"
	case "Offline":
		if s.Error != "" {
			return "连接失败"
		}
		return "已断开"
	default:
		return "尚未加入"
	}
}
func tailnetLatency(p tailnet.Peer, now time.Time) string {
	m := p.Measurement
	if m == nil {
		return "尚未测量"
	}
	prefix := ""
	if !p.Online || now.Sub(m.ObservedAt) > 30*time.Second {
		prefix = "上次 "
	}
	return prefix + fmt.Sprintf("%.1f ms", m.Milliseconds)
}
func (v *View) connectionPeer(key string) (tailnet.Peer, bool) {
	for _, connection := range v.Model.Connections {
		ssh := connection.ActiveSSH()
		if connection.ID == key && ssh.TailnetPeer != "" {
			return v.tailnetPathPeer(ssh.TailnetPeer), true
		}
	}
	return tailnet.Peer{}, false
}

func (v *View) tailnetPathPeer(id string) tailnet.Peer {
	for _, p := range v.Model.Tailnet.Peers {
		if p.ID == id {
			if v.Model.Tailnet.State != "Running" {
				p.Online = false
			}
			return p
		}
	}
	return tailnet.Peer{ID: id}
}

func (v *View) setupWorkerSSH(port int, info transport.WorkerInfo) transport.SSH {
	s := v.setupSSH(port)
	s.Worker = info.Worker
	s.StateDirectory = info.Directory
	return s
}
func setupWorkerSSHPointer(s transport.SSH) *transport.SSH { return &s }

func (v *View) tailnetPathDetails(c *n.Context, peerID string) {
	if peerID != "" {
		p := v.tailnetPathPeer(peerID)
		if p.Name != "" {
			deviceMeta(c, "Tailnet 名称", p.Name)
		}
		deviceMeta(c, "往返延迟", tailnetLatency(p, c.Now()))
		deviceMeta(c, "连接路径", tailnetPathLabel(p, c.Now()))
	}

}

func (u *tailnetUI) authorizationURL(s tailnet.Snapshot) string {
	if !u.joining || !s.Authorizing || s.OperationID != u.joinID || s.OperationID == u.canceledID {
		return ""
	}
	return s.AuthURL
}

func (u *tailnetUI) pending(s tailnet.Snapshot) bool {
	return u.joining && (s.OperationID != u.joinID || s.Error == "" || s.Authorizing) && !(s.OperationID == u.joinID && s.State == "Running" && !s.Authorizing)
}

func (v *View) tailnetHeading(c *n.Context, t *n.Theme, title string, compact bool) {
	size, line := float32(14), float32(20)
	if !compact {
		size, line = 20, 28
	}
	n.Row(c).FillWidth().MinHeight(line).Margin(0, 0, 12).AlignItems(n.Baseline).Children(func() { n.Text(c, title).FontSize(size).FontWeight(400).FixedLineHeight(line) })
}

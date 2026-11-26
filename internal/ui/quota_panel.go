package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// quickQuotaIcon shares the 2…18 ink extent, 1.5 stroke, and optical center of
// the left-sidebar dock icons in a 20-unit viewBox.
var quickQuotaIcon = svg("M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0M10 10l3.5-3.5M10 5v1M6 10h1")
var quickQuotaPickIcon = svg("M3 6h14M3 14h14M6 4v4M14 12v4")

const (
	quickQuotaMaxHeight       float32 = 310
	quickQuotaPickerMaxHeight float32 = 360
)

func (v *View) saveQuotaState() {
	root := v.splitRoot()
	if root.Saver != nil {
		root.queueState()
	}
}

func (v *View) stopQuotaTimers() {
	if v.globalSidebar == nil {
		return
	}
	if v.globalSidebar.quotaTimer != nil {
		v.globalSidebar.quotaTimer.Stop()
		v.globalSidebar.quotaTimer = nil
	}
	v.globalSidebar.quotaTimerAt = time.Time{}
}

func (v *View) windowVisibleForQuota() bool {
	if v.nativeWindow == nil {
		return true
	}
	return !v.nativeWindow.IsDestroyed() && v.nativeWindow.IsVisible() && !v.nativeWindow.IsMinimized()
}

func (v *View) quotaRefreshableTargets() []app.QuotaProfileDescriptor {
	pm := v.persistenceModel()
	prefs := pm.QuotaPanel.Normalized()
	var out []app.QuotaProfileDescriptor
	for _, target := range prefs.Targets {
		desc := pm.ResolveQuotaProfile(target)
		if desc.Connected && !desc.WorkerLegacy {
			out = append(out, desc)
		}
	}
	return out
}

func (v *View) fetchQuotaTarget(desc app.QuotaProfileDescriptor, force bool, now time.Time) bool {
	if !desc.Connected || desc.WorkerLegacy {
		return false
	}
	pm := v.persistenceModel()
	if !force {
		if st := pm.QuotaState(desc.Target); st != nil && st.Loading {
			return false
		}
	}
	pm.BeginQuotaFetch(desc.Target, now)
	target := desc.Target
	kind := desc.Kind
	sent := v.do(app.Job{
		ConnectionID: target.ConnectionID,
		Method:       "runtime.quota",
		Input:        protocol.QuotaQuery{HarnessID: target.ProfileID, Force: force},
		Apply: func(m *app.Model, body json.RawMessage, err error) {
			var res protocol.QuotaResult
			if err == nil && len(body) > 0 {
				if decErr := json.Unmarshal(body, &res); decErr != nil {
					err = decErr
				}
			}
			m.ApplyQuotaResult(target, kind, res, err, time.Now())
		},
	})
	if !sent {
		if st := pm.QuotaState(target); st != nil {
			st.Loading = false
		}
	}
	return sent
}

func (v *View) refreshAllQuotas(force bool, now time.Time) {
	targets := v.quotaRefreshableTargets()
	for _, desc := range targets {
		v.fetchQuotaTarget(desc, force, now)
	}
}

// syncQuotaScheduler runs scheduled polls and countdown refreshes strictly while
// the window is visible and the left-sidebar quota drawer is open.
func (v *View) syncQuotaScheduler(c *n.Context) {
	s := v.quickUI()
	pm := v.persistenceModel()
	prefs := pm.QuotaPanel.Normalized()
	if s.selected != "quota" || !v.windowVisibleForQuota() || len(prefs.Targets) == 0 {
		v.stopQuotaTimers()
		return
	}
	now := c.Now()

	// Enforce the 45s batch ceiling on any stuck in-flight fetch on the UI thread.
	for _, target := range prefs.Targets {
		st := pm.QuotaState(target)
		if st != nil && st.Loading && !st.LastAttemptAt.IsZero() && now.Sub(st.LastAttemptAt) >= app.QuotaBatchCeiling {
			desc := pm.ResolveQuotaProfile(target)
			pm.ApplyQuotaResult(target, desc.Kind, protocol.QuotaResult{}, errors.New("额度刷新超时"), now)
		}
	}

	if v.Controller != nil {
		if due := pm.DueQuotaTargets(now); len(due) > 0 {
			for _, desc := range due {
				v.fetchQuotaTarget(desc, false, now)
			}
		}
	}

	// Compute the next wakeup needed for scheduled polls, backoff expiry,
	// batch ceiling timeout, or visible countdown freshness.
	nextDelay := 15 * time.Second
	shorten := func(d time.Duration) {
		if d < time.Second {
			d = time.Second
		}
		if d < nextDelay {
			nextDelay = d
		}
	}
	for _, target := range prefs.Targets {
		st := pm.QuotaState(target)
		if st == nil {
			continue
		}
		if st.Loading && !st.LastAttemptAt.IsZero() {
			rem := app.QuotaBatchCeiling - now.Sub(st.LastAttemptAt)
			shorten(rem)
		}
		if !st.BackoffUntil.IsZero() && st.BackoffUntil.After(now) {
			rem := st.BackoffUntil.Sub(now)
			if rem <= 2*time.Minute {
				shorten(time.Second)
			} else {
				shorten(rem)
			}
		}
		if !st.NextRefreshAt.IsZero() && st.NextRefreshAt.After(now) {
			shorten(st.NextRefreshAt.Sub(now))
		}
		if st.Snapshot != nil {
			if nearReset(st.Snapshot, now) {
				shorten(time.Second)
			}
		}
	}

	targetAt := now.Add(nextDelay)
	if s.quotaTimer == nil || s.quotaTimerAt.IsZero() || targetAt.Before(s.quotaTimerAt.Add(-250*time.Millisecond)) || !s.quotaTimerAt.After(now) {
		if s.quotaTimer != nil {
			s.quotaTimer.Stop()
		}
		s.quotaTimerAt = targetAt
		invalidate := c.Services().Invalidate
		s.quotaTimer = time.AfterFunc(nextDelay, func() {
			invalidate()
		})
	}
}

func nearReset(snap *domain.QuotaSnapshot, now time.Time) bool {
	if snap == nil {
		return false
	}
	check := func(t *time.Time) bool {
		if t == nil {
			return false
		}
		d := t.Sub(now)
		return d > 0 && d <= 2*time.Minute
	}
	for _, w := range snap.Windows {
		if check(w.ResetAt) {
			return true
		}
	}
	for _, g := range snap.Groups {
		for _, w := range g.Windows {
			if check(w.ResetAt) {
				return true
			}
		}
	}
	if snap.SpendLimit != nil && check(snap.SpendLimit.ResetAt) {
		return true
	}
	return false
}

func (v *View) quickQuotaHeight() float32 {
	s := v.quickUI()
	pm := v.persistenceModel()
	if s.quotaPickerOpen {
		catalog := pm.QuotaCatalog()
		rows := float32(72) // heading + local-only note
		for _, dev := range catalog {
			rows += 28
			if len(dev.Profiles) == 0 {
				rows += 28
			} else {
				rows += float32(len(dev.Profiles)) * 34
			}
		}
		return min(quickQuotaPickerMaxHeight, max(168, rows))
	}
	prefs := pm.QuotaPanel.Normalized()
	if len(prefs.Targets) == 0 {
		return 164
	}
	tabBarH := float32(0)
	if len(prefs.Targets) >= 2 {
		tabBarH = 30
	}
	activeTab := prefs.ActiveTab
	if len(prefs.Targets) == 1 {
		activeTab = prefs.Targets[0].Key()
	} else if activeTab == "" {
		activeTab = "all"
	}
	if activeTab == "all" && len(prefs.Targets) >= 2 {
		return min(quickQuotaMaxHeight, 44+tabBarH+float32(len(prefs.Targets))*48+10)
	}
	var selectedTarget app.QuotaTarget
	for _, t := range prefs.Targets {
		if t.Key() == activeTab {
			selectedTarget = t
			break
		}
	}
	if selectedTarget.ProfileID == "" {
		selectedTarget = prefs.Targets[0]
	}
	h := float32(44) + tabBarH + 34 // heading + optional tab bar + profile context header
	st := pm.QuotaState(selectedTarget)
	if st == nil || st.Snapshot == nil {
		return min(quickQuotaMaxHeight, h+64)
	}
	snap := st.Snapshot
	if st.Error != "" {
		h += 38
	}
	if snap.SpendControlReached || (snap.OrdinaryUsageAllowed != nil && !*snap.OrdinaryUsageAllowed) {
		h += 24
	}
	if snap.Balance != nil {
		entries := max(1, len(snap.Balance.Entries))
		h += float32(entries)*42 + 8
	}
	h += float32(len(snap.Windows)) * 38
	for _, g := range snap.Groups {
		h += 20 + float32(len(g.Windows))*36
	}
	if snap.SpendLimit != nil {
		h += 38
	}
	if snap.Credits != nil && (snap.Credits.Unlimited || snap.Credits.HasCredits || snap.Credits.Balance != "") {
		h += 26
	}
	return min(quickQuotaMaxHeight, max(136, h+14))
}

func (v *View) quickQuotaHeadingStatus(now time.Time) string {
	pm := v.persistenceModel()
	prefs := pm.QuotaPanel.Normalized()
	if len(prefs.Targets) == 0 {
		return ""
	}
	anyLoading := false
	var latest time.Time
	for _, target := range prefs.Targets {
		st := pm.QuotaState(target)
		if st == nil {
			continue
		}
		if st.Loading {
			anyLoading = true
		}
		if st.LastSuccessAt.After(latest) {
			latest = st.LastSuccessAt
		}
	}
	if anyLoading {
		return "刷新中…"
	}
	if !latest.IsZero() {
		return formatRelativeAgo(now.Sub(latest))
	}
	return ""
}

func (v *View) quickQuota(c *n.Context, t *n.Theme) {
	s := v.quickUI()
	pm := v.persistenceModel()
	prefs := pm.QuotaPanel.Normalized()
	if s.quotaPickerOpen {
		v.quickQuotaPicker(c, t, pm, prefs)
		return
	}
	if len(prefs.Targets) == 0 {
		v.quickQuotaEmpty(c, t)
		return
	}

	profiles := make([]app.QuotaProfileDescriptor, len(prefs.Targets))
	for i, target := range prefs.Targets {
		profiles[i] = pm.ResolveQuotaProfile(target)
	}
	tabLabels := app.QuotaTabLabels(profiles)

	type quotaTab struct {
		key     string
		label   string
		profile *app.QuotaProfileDescriptor
	}
	var tabs []quotaTab
	if len(profiles) >= 2 {
		tabs = append(tabs, quotaTab{key: "all", label: fmt.Sprintf("全部 %d", len(profiles))})
	}
	for i := range profiles {
		p := profiles[i]
		tabs = append(tabs, quotaTab{key: p.Target.Key(), label: tabLabels[i], profile: &p})
	}

	activeTab := prefs.ActiveTab
	if len(profiles) == 1 {
		activeTab = profiles[0].Target.Key()
	} else if activeTab == "" {
		activeTab = "all"
	}
	activeIdx := 0
	found := false
	for i, tab := range tabs {
		if tab.key == activeTab {
			activeIdx = i
			found = true
			break
		}
	}
	if !found && len(tabs) > 0 {
		activeTab = tabs[0].key
		activeIdx = 0
	}

	body := n.Column(c.Key("quota-body")).Grow(1).FillWidth().MinHeight(0)
	if s.selected == "quota" && len(tabs) >= 2 {
		if body.Shortcut(0, n.KeyLeft) && activeIdx > 0 {
			next := tabs[activeIdx-1].key
			pm.SetQuotaActiveTab(next)
			v.saveQuotaState()
			activeTab, activeIdx = next, activeIdx-1
		}
		if body.Shortcut(0, n.KeyRight) && activeIdx+1 < len(tabs) {
			next := tabs[activeIdx+1].key
			pm.SetQuotaActiveTab(next)
			v.saveQuotaState()
			activeTab, activeIdx = next, activeIdx+1
		}
	}

	tabChanged := s.quotaLastTab != activeTab
	s.quotaLastTab = activeTab

	body.Children(func() {
		if len(tabs) >= 2 {
			// Tight bottom spacing (4px) keeps tabs visually anchored to the content below,
			// with zero divider lines for a calm borderless sidebar aesthetic.
			scroller := n.ScrollHorizontal(c.Key("quota-tab-scroll")).TrackScroll(&s.quotaTabs).Scrollbars(false).FillWidth().Shrink(0).Height(28).Label("额度 Profile 切换栏")
			scroller.Children(func() {
				idx := activeIdx
				parts := n.TabsBase(c.Key("quota-tabs"), &idx, len(tabs))
				list := parts.List.Height(24).Padding(0, 12).Gap(4).AlignItems(n.Center)
				list.Children(func() {
					for i, tab := range tabs {
						selected := i == activeIdx
						el := parts.Tab(i).Height(22).Padding(0, 8).Radius(6).Shrink(0).Label(tab.label)
						if selected {
							el.Background(t.SurfacePressed).TextColor(t.Text)
						} else {
							el.TextColor(t.TextMuted)
							controlFeedback(c, el, 6)
						}
						if selected && tabChanged {
							el.ScrollIntoView(n.ScrollIntoViewOptions{Inline: n.ScrollNearest, Animation: &n.Smooth})
						}
						el.Children(func() {
							n.Row(c).Gap(5).AlignItems(n.Center).Children(func() {
								if !selected && tab.profile != nil {
									if sev := pm.TargetSeverity(*tab.profile); sev != app.QuotaSeverityNone {
										n.Box(c).Size(5, 5).Radius(3).Background(quotaSeverityColor(t, sev)).Role(n.RoleNone)
									}
								}
								weight := 400
								if selected {
									weight = 500
								}
								n.Text(c, tab.label).FontSize(11).FontWeight(weight).FixedLineHeight(16).SingleLine()
							})
						})
					}
				})
				if list.Changed() && idx >= 0 && idx < len(tabs) {
					next := tabs[idx].key
					pm.SetQuotaActiveTab(next)
					v.saveQuotaState()
					activeTab, activeIdx = next, idx
				}
			})
		}

		// Directional left/right slide transition between tabs.
		configurationTabPanel(c.Key("quota-tab-panel"), activeTab, activeIdx, configurationPanelFill, func() {
			n.Scroll(c.Key("quota-content-scroll")).TrackScroll(&s.quotaScroll).Grow(1).FillWidth().MinHeight(0).Children(func() {
				if activeTab == "all" && len(profiles) >= 2 {
					v.quickQuotaAllTab(c, t, pm, profiles, tabLabels)
					return
				}
				var current app.QuotaProfileDescriptor
				if activeIdx >= 0 && activeIdx < len(tabs) && tabs[activeIdx].profile != nil {
					current = *tabs[activeIdx].profile
				} else {
					current = profiles[0]
				}
				v.quickQuotaProfileDetail(c, t, pm, current, len(profiles) >= 2)
			})
		}, false)
	})
}

func (v *View) quickQuotaEmpty(c *n.Context, t *n.Theme) {
	n.Column(c.Key("quota-empty")).Grow(1).FillWidth().Padding(16, 12, 22, 12).Gap(10).AlignItems(n.Center).Justify(n.Center).Children(func() {
		n.Icon(c, quickQuotaIcon).Size(24, 24).TextColor(t.TextMuted).IgnoreInput(true).Role(n.RoleNone)
		btn := control(c.Key("quota-open-picker"), 6).Height(28).Padding(0, 12).Radius(6).Background(t.SurfacePressed).Label("选择 Profile")
		btn.Children(func() {
			n.Text(c, "选择 Profile").FontSize(12).FontWeight(500).FixedLineHeight(18).TextColor(t.Text)
		})
		if btn.Clicked() {
			v.quickUI().quotaPickerOpen = true
		}
	})
}

func (v *View) quickQuotaPicker(c *n.Context, t *n.Theme, pm *app.Model, prefs app.QuotaPanelPreferences) {
	s := v.quickUI()
	catalog := pm.QuotaCatalog()
	n.Column(c.Key("quota-picker")).Grow(1).FillWidth().MinHeight(0).Children(func() {
		n.Text(c, "配置仅保存在本机，不同步到其他设备").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Padding(0, 12, 6, 12).Shrink(0)
		n.Scroll(c.Key("quota-picker-scroll")).TrackScroll(&s.quotaPickerList).Grow(1).FillWidth().MinHeight(0).Label("可选择的额度 Profile 列表").Children(func() {
			n.Column(c).FillWidth().Padding(0, 8, 10, 8).Gap(6).Children(func() {
				for _, dev := range catalog {
					n.Column(c.Key("dev:"+dev.ConnectionID)).FillWidth().Gap(2).Children(func() {
						n.Row(c).Height(22).FillWidth().Padding(0, 4).Gap(6).AlignItems(n.Center).Children(func() {
							dotColor := t.TextMuted
							if dev.Connected {
								dotColor = t.Success
							}
							n.Box(c).Size(5, 5).Radius(3).Background(dotColor).Role(n.RoleNone)
							n.Text(c, dev.DeviceName).FontSize(11).FontWeight(500).FixedLineHeight(16).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
							if !dev.Connected {
								n.Text(c, "离线").FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted).Shrink(0)
							} else if !dev.WorkerSupport {
								n.Text(c, "需升级").FontSize(10).FixedLineHeight(14).TextColor(t.Warning).Shrink(0)
							}
						})
						if len(dev.Profiles) == 0 {
							msg := "暂无支持的 Profile"
							if !dev.Connected {
								msg = "设备离线，连接后可发现 Profile"
							}
							n.Text(c, msg).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Padding(2, 16, 4, 16)
							return
						}
						for _, p := range dev.Profiles {
							checked := prefs.Has(p.Target)
							row := control(c.Key("pick:"+p.Target.Key()), 6).FillWidth().Height(32).Padding(0, 8).Label("选择 " + p.Label + "（" + dev.DeviceName + "）").Selected(checked)
							if checked {
								row.Background(t.SurfaceHover)
							}
							row.Children(func() {
								n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
									box := n.Row(c).Size(14, 14).Radius(3).AlignItems(n.Center).Justify(n.Center).Shrink(0)
									if checked {
										box.Background(t.Accent)
										box.Children(func() {
											n.Icon(c, check).Size(10, 10).TextColor(t.AccentText).Role(n.RoleNone)
										})
									} else {
										box.Border(1, t.Border)
									}
									n.Icon(c, harnessIcon(quotaHarnessDescriptor(p))).Size(14, 14).TextColor(t.TextMuted).Shrink(0).Role(n.RoleNone)
									n.Text(c, p.Label).FontSize(12).FixedLineHeight(18).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(p.Label)
									if tag := quotaKindTag(p.Kind); tag != "" {
										n.Text(c, tag).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted).Shrink(0)
									}
								})
							})
							if row.Clicked() {
								added := pm.ToggleQuotaTarget(p.Target)
								v.saveQuotaState()
								if added && p.Connected && !p.WorkerLegacy && v.Controller != nil {
									v.fetchQuotaTarget(p, false, c.Now())
								}
							}
						}
					})
				}
			})
		})
	})
}

func (v *View) quickQuotaAllTab(c *n.Context, t *n.Theme, pm *app.Model, profiles []app.QuotaProfileDescriptor, tabLabels []string) {
	now := c.Now()
	n.Column(c.Key("quota-all-list")).FillWidth().Padding(2, 8, 10, 8).Gap(2).Children(func() {
		for i, desc := range profiles {
			st := pm.QuotaState(desc.Target)
			sev := pm.TargetSeverity(desc)
			row := control(c.Key("all:"+desc.Target.Key()), 6).FillWidth().Padding(6, 8).Label("查看 " + tabLabels[i] + " 额度详情")
			row.Children(func() {
				n.Column(c).FillWidth().Gap(2).Children(func() {
					n.Row(c).FillWidth().Height(18).Gap(6).AlignItems(n.Center).Children(func() {
						dotColor := t.Success
						if !desc.Connected {
							dotColor = t.TextMuted
						} else if sev != app.QuotaSeverityNone {
							dotColor = quotaSeverityColor(t, sev)
						}
						n.Box(c).Size(5, 5).Radius(3).Background(dotColor).Shrink(0).Role(n.RoleNone)
						n.Text(c, tabLabels[i]).FontSize(12).FontWeight(500).FixedLineHeight(18).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(desc.Label + " · " + desc.DeviceName)
						summary, summaryColor := quotaCompactSummary(t, desc, st)
						n.Text(c, summary).FontSize(11).FontWeight(500).FixedLineHeight(18).TextColor(summaryColor).Shrink(0)
					})
					if sub := quotaCompactSubtitle(desc, st, now); sub != "" {
						n.Text(c, sub).FontSize(10).FixedLineHeight(14).PaddingStart(11).FillWidth().SingleLine().Ellipsis("…").TextColor(t.TextMuted).TooltipWhenTruncated(sub)
					}
				})
			})
			if row.Clicked() {
				pm.SetQuotaActiveTab(desc.Target.Key())
				v.saveQuotaState()
			}
		}
	})
}

func (v *View) quickQuotaProfileDetail(c *n.Context, t *n.Theme, pm *app.Model, desc app.QuotaProfileDescriptor, hasTabs bool) {
	st := pm.QuotaState(desc.Target)
	now := c.Now()
	topPad := float32(2)
	if !hasTabs {
		topPad = 0
	}

	n.Column(c.Key("detail:"+desc.Target.Key())).FillWidth().Padding(topPad, 12, 12, 12).Gap(8).Children(func() {
		// Compact profile context line directly anchored to the tab bar (no heavy header gap).
		n.Row(c).FillWidth().Height(20).Gap(6).AlignItems(n.Center).Children(func() {
			n.Icon(c, harnessIcon(quotaHarnessDescriptor(desc))).Size(14, 14).TextColor(t.TextMuted).Shrink(0).Role(n.RoleNone)
			n.Text(c, desc.Label).FontSize(12).FontWeight(500).FixedLineHeight(18).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).TooltipWhenTruncated(desc.Label)
			if desc.DeviceName != "" {
				n.Text(c, "· "+desc.DeviceName).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(desc.DeviceName)
			} else {
				n.Spacer(c).Grow(1)
			}
			if st != nil && st.Snapshot != nil {
				if plan := strings.TrimSpace(st.Snapshot.PlanType); plan != "" {
					n.Text(c, formatPlanBadge(plan)).FontSize(10).FixedLineHeight(14).Padding(1, 5).Radius(4).Background(t.SurfacePressed).TextColor(t.TextMuted).Shrink(0)
				}
				if st.Snapshot.Source == domain.QuotaSourcePush {
					n.Box(c).Size(5, 5).Radius(3).Background(t.Success).Tooltip("实时推送更新").Shrink(0)
				}
			}
		})

		if !desc.Connected && (st == nil || st.Snapshot == nil) {
			n.Text(c, "设备已离线，连接后自动获取额度").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
			return
		}
		if desc.WorkerLegacy {
			n.Text(c, "该设备执行服务版本较旧，升级后可查看额度").FontSize(11).FixedLineHeight(16).TextColor(t.Warning)
			return
		}

		// Stale/error warning bar (preserves cached snapshot dimmed below when available).
		if st != nil && st.Error != "" {
			v.quickQuotaErrorBar(c, t, desc, st, now)
		} else if !desc.Connected && st != nil && st.Snapshot != nil {
			n.Text(c, "设备离线，显示上次缓存数据").FontSize(11).FixedLineHeight(16).TextColor(t.Warning)
		}

		if st == nil || st.Snapshot == nil {
			if st != nil && st.Error != "" {
				return
			}
			msg := "正在获取额度…"
			if st != nil && !st.Loading {
				msg = "等待刷新额度"
			}
			n.Text(c, msg).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
			return
		}

		snap := st.Snapshot
		stale := !desc.Connected || st.Error != ""
		content := n.Column(c.Key("snapshot-body")).FillWidth().Gap(8)
		if stale {
			content.Opacity(0.58)
		}
		content.Children(func() {
			if snap.SpendControlReached || (snap.OrdinaryUsageAllowed != nil && !*snap.OrdinaryUsageAllowed) {
				n.Text(c, "已达到用量上限").FontSize(11).FontWeight(500).FixedLineHeight(16).Padding(2, 6).Radius(4).Background(t.Danger.Alpha(0.14)).TextColor(t.Danger).AlignSelf(n.Start)
			}

			// Balance-only provider (DeepSeek): render currency balances only, no progress bar or reset time.
			if snap.Balance != nil {
				v.quickQuotaBalance(c, t, snap.Balance)
			}

			// Dynamically returned top-level rate-limit windows (only render what actually exists).
			for i, w := range snap.Windows {
				quotaWindowRow(c.Key(fmt.Sprintf("w:%d:%s", i, w.ID)), t, w, now)
			}

			// Dynamically returned window groups (Antigravity model groups or Codex rateLimitsByLimitId).
			for gi, g := range snap.Groups {
				if len(g.Windows) == 0 {
					continue
				}
				n.Column(c.Key(fmt.Sprintf("g:%d:%s", gi, g.ID))).FillWidth().Gap(5).Children(func() {
					groupTitle := strings.TrimSpace(g.Name)
					if groupTitle == "" {
						groupTitle = strings.TrimSpace(g.LimitID)
					}
					if groupTitle != "" {
						n.Text(c, groupTitle).FontSize(11).FontWeight(500).FixedLineHeight(15).TextColor(t.TextMuted).SingleLine().Ellipsis("…").TooltipWhenTruncated(groupTitle)
					}
					for wi, w := range g.Windows {
						quotaWindowRow(c.Key(fmt.Sprintf("gw:%d:%s", wi, w.ID)), t, w, now)
					}
				})
			}

			// Codex spend limit (if returned).
			if snap.SpendLimit != nil {
				quotaSpendLimitRow(c.Key("spend-limit"), t, snap.SpendLimit, now)
			}

			// Codex credits (if returned).
			if snap.Credits != nil && (snap.Credits.Unlimited || snap.Credits.HasCredits || strings.TrimSpace(snap.Credits.Balance) != "") {
				value := strings.TrimSpace(snap.Credits.Balance)
				if snap.Credits.Unlimited {
					value = "无限制"
				} else if value == "" {
					value = "可用"
				}
				n.Row(c.Key("credits")).FillWidth().Height(18).Gap(8).AlignItems(n.Center).Children(func() {
					n.Text(c, "Credits").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
					n.Spacer(c).Grow(1)
					n.Text(c, value).FontSize(12).FontWeight(500).FixedLineHeight(16).TextColor(t.Text)
				})
			}
		})
	})
}

func (v *View) quickQuotaErrorBar(c *n.Context, t *n.Theme, desc app.QuotaProfileDescriptor, st *app.QuotaProfileState, now time.Time) {
	accent := t.Warning
	if st.AuthUnavailable {
		accent = t.Danger
	}
	n.Row(c.Key("quota-err")).FillWidth().Padding(6, 8).Radius(6).Background(accent.Alpha(0.10)).Gap(8).AlignItems(n.Center).Children(func() {
		n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			n.Text(c, st.Error).FontSize(11).FixedLineHeight(15).TextColor(accent).TooltipWhenTruncated(st.Error)
			if !st.BackoffUntil.IsZero() && st.BackoffUntil.After(now) {
				n.Text(c, fmt.Sprintf("%s 后自动重试", formatCountdown(st.BackoffUntil.Sub(now)))).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted)
			}
		})
		if desc.Connected && !desc.WorkerLegacy && !st.Unsupported {
			retry := control(c.Key("retry"), 5).Height(22).Padding(0, 8).Background(t.Surface).Label("重试刷新额度").Disabled(st.Loading)
			retry.Children(func() {
				label := "重试"
				if st.Loading {
					label = "刷新中"
				}
				n.Text(c, label).FontSize(11).FontWeight(500).FixedLineHeight(16).TextColor(t.Text)
			})
			if retry.Clicked() {
				v.fetchQuotaTarget(desc, true, now)
			}
		}
	})
}

func (v *View) quickQuotaBalance(c *n.Context, t *n.Theme, balance *domain.QuotaBalance) {
	n.Column(c.Key("balance")).FillWidth().Gap(6).Children(func() {
		if !balance.Available {
			n.Text(c, "余额不可用").FontSize(11).FontWeight(500).FixedLineHeight(16).Padding(2, 6).Radius(4).Background(t.Danger.Alpha(0.14)).TextColor(t.Danger).AlignSelf(n.Start)
		}
		if len(balance.Entries) == 0 {
			n.Text(c, "暂无余额明细").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
			return
		}
		for i, entry := range balance.Entries {
			n.Column(c.Key(fmt.Sprintf("bal:%d:%s", i, entry.Currency))).FillWidth().Gap(2).Children(func() {
				n.Row(c).FillWidth().AlignItems(n.Baseline).Gap(6).Children(func() {
					label := "可用余额"
					if len(balance.Entries) > 1 && entry.Currency != "" {
						label = "余额 (" + entry.Currency + ")"
					}
					n.Text(c, label).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
					n.Spacer(c).Grow(1)
					n.Text(c, formatCurrencyAmount(entry.Currency, entry.TotalBalance)).FontSize(15).FontWeight(600).FixedLineHeight(20).TextColor(t.Text)
				})
				var parts []string
				if topped := strings.TrimSpace(entry.ToppedUpBalance); topped != "" {
					parts = append(parts, "充值 "+formatCurrencyAmount(entry.Currency, topped))
				}
				if granted := strings.TrimSpace(entry.GrantedBalance); granted != "" {
					parts = append(parts, "赠金 "+formatCurrencyAmount(entry.Currency, granted))
				}
				if len(parts) > 0 {
					n.Text(c, strings.Join(parts, " · ")).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted)
				}
			})
		}
	})
}

func quotaWindowRow(c *n.Context, t *n.Theme, w domain.QuotaWindow, now time.Time) {
	rem, hasPct := w.EffectiveRemainingPercent()
	label := w.DisplayLabel()
	pctColor := t.Text
	barColor := t.Accent
	if hasPct {
		switch {
		case rem <= 5:
			pctColor, barColor = t.Danger, t.Danger
		case rem <= 20:
			pctColor, barColor = t.Warning, t.Warning
		}
	}
	n.Column(c).FillWidth().Gap(4).Children(func() {
		n.Row(c).FillWidth().Height(16).Gap(6).AlignItems(n.Baseline).Children(func() {
			n.Text(c, label).FontSize(11).FixedLineHeight(16).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(label)
			if w.ResetAt != nil {
				resetText := formatResetTime(*w.ResetAt, now)
				if resetText != "" {
					n.Text(c, resetText).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted).Shrink(0)
				}
			}
			if hasPct {
				n.Text(c, fmt.Sprintf("%d%%", int(math.Round(rem)))).FontSize(11).FontWeight(500).FixedLineHeight(16).TextColor(pctColor).Shrink(0)
			}
		})
		if hasPct {
			quotaProgressBar(c, t, float32(rem/100), barColor)
		}
	})
}

func quotaSpendLimitRow(c *n.Context, t *n.Theme, spend *domain.QuotaSpendLimit, now time.Time) {
	n.Column(c).FillWidth().Gap(4).Children(func() {
		n.Row(c).FillWidth().Height(16).Gap(6).AlignItems(n.Baseline).Children(func() {
			n.Text(c, "消费限额").FontSize(11).FixedLineHeight(16).TextColor(t.Text).Shrink(0)
			if spend.Used != "" || spend.Limit != "" {
				detail := strings.TrimSpace(spend.Used)
				if spend.Limit != "" {
					if detail != "" {
						detail += " / " + spend.Limit
					} else {
						detail = spend.Limit
					}
				}
				n.Text(c, detail).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			} else {
				n.Spacer(c).Grow(1)
			}
			if spend.ResetAt != nil {
				if rt := formatResetTime(*spend.ResetAt, now); rt != "" {
					n.Text(c, rt).FontSize(10).FixedLineHeight(14).TextColor(t.TextMuted).Shrink(0)
				}
			}
			if spend.RemainingPercent != nil {
				rem := *spend.RemainingPercent
				pctColor := t.Text
				if rem <= 5 {
					pctColor = t.Danger
				} else if rem <= 20 {
					pctColor = t.Warning
				}
				n.Text(c, fmt.Sprintf("%d%%", rem)).FontSize(11).FontWeight(500).FixedLineHeight(16).TextColor(pctColor).Shrink(0)
			}
		})
		if spend.RemainingPercent != nil {
			rem := float32(*spend.RemainingPercent) / 100
			if rem < 0 {
				rem = 0
			}
			if rem > 1 {
				rem = 1
			}
			barColor := t.Accent
			if *spend.RemainingPercent <= 5 {
				barColor = t.Danger
			} else if *spend.RemainingPercent <= 20 {
				barColor = t.Warning
			}
			quotaProgressBar(c, t, rem, barColor)
		}
	})
}

func quotaProgressBar(c *n.Context, t *n.Theme, ratio float32, fill n.Color) {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	track := t.SurfacePressed
	n.Box(c).FillWidth().Height(4).Radius(2).Draw(func(p *n.Painter, r n.Rect) {
		p.Fill(r, track, 2)
		if ratio > 0 {
			w := r.W * ratio
			if w < 3 {
				w = min(3, r.W)
			}
			p.Fill(n.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, fill, 2)
		}
	}).Role(n.RoleNone)
}

func quotaSeverityColor(t *n.Theme, sev app.QuotaSeverity) n.Color {
	switch sev {
	case app.QuotaSeverityCritical:
		return t.Danger
	case app.QuotaSeverityWarning:
		return t.Warning
	default:
		return t.Success
	}
}

func quotaHarnessDescriptor(desc app.QuotaProfileDescriptor) domain.HarnessDescriptor {
	if desc.Harness.ID != "" {
		return domain.HarnessDescriptor{ID: desc.Harness.ID, Name: desc.Harness.Name, Icon: desc.Harness.Icon}
	}
	return domain.HarnessDescriptor{ID: desc.Kind, Name: desc.Label}
}

func quotaKindTag(kind string) string {
	switch kind {
	case domain.QuotaKindCodex:
		return "订阅"
	case domain.QuotaKindAntigravity:
		return "系统"
	case domain.QuotaKindDeepSeek:
		return "余额"
	default:
		return ""
	}
}

func quotaCompactSummary(t *n.Theme, desc app.QuotaProfileDescriptor, st *app.QuotaProfileState) (string, n.Color) {
	if !desc.Connected && (st == nil || st.Snapshot == nil) {
		return "离线", t.TextMuted
	}
	if desc.WorkerLegacy {
		return "需升级", t.Warning
	}
	if st == nil {
		return "—", t.TextMuted
	}
	if st.Snapshot == nil {
		if st.Loading {
			return "刷新中…", t.TextMuted
		}
		if st.Error != "" {
			if st.AuthUnavailable {
				return "未登录", t.Danger
			}
			return "获取失败", t.Warning
		}
		return "—", t.TextMuted
	}
	snap := st.Snapshot
	if snap.Balance != nil {
		if len(snap.Balance.Entries) > 0 {
			e := snap.Balance.Entries[0]
			col := t.Text
			if !snap.Balance.Available {
				col = t.Danger
			}
			return formatCurrencyAmount(e.Currency, e.TotalBalance), col
		}
		if !snap.Balance.Available {
			return "不可用", t.Danger
		}
	}
	if w, _, ok := app.SnapshotTightestWindow(*snap); ok {
		rem, _ := w.EffectiveRemainingPercent()
		col := t.Text
		if rem <= 5 {
			col = t.Danger
		} else if rem <= 20 {
			col = t.Warning
		}
		lbl := w.DisplayLabel()
		if lbl != "" && lbl != "额度窗口" && len([]rune(lbl)) <= 8 {
			return fmt.Sprintf("%s %d%%", lbl, int(math.Round(rem))), col
		}
		return fmt.Sprintf("%d%%", int(math.Round(rem))), col
	}
	if snap.SpendLimit != nil && snap.SpendLimit.RemainingPercent != nil {
		rem := *snap.SpendLimit.RemainingPercent
		col := t.Text
		if rem <= 5 {
			col = t.Danger
		} else if rem <= 20 {
			col = t.Warning
		}
		return fmt.Sprintf("%d%%", rem), col
	}
	if snap.Credits != nil && snap.Credits.Unlimited {
		return "无限制", t.Text
	}
	if snap.Credits != nil && snap.Credits.Balance != "" {
		return snap.Credits.Balance, t.Text
	}
	return "正常", t.TextMuted
}

func quotaCompactSubtitle(desc app.QuotaProfileDescriptor, st *app.QuotaProfileState, now time.Time) string {
	var parts []string
	if desc.DeviceName != "" {
		parts = append(parts, desc.DeviceName)
	}
	if st != nil && st.Error != "" {
		parts = append(parts, "刷新失败")
		return strings.Join(parts, " · ")
	}
	if !desc.Connected {
		parts = append(parts, "离线")
		return strings.Join(parts, " · ")
	}
	if st != nil && st.Snapshot != nil {
		if w, group, ok := app.SnapshotTightestWindow(*st.Snapshot); ok {
			if group != "" {
				parts = append(parts, group)
			}
			if w.ResetAt != nil {
				if rt := formatResetTime(*w.ResetAt, now); rt != "" {
					parts = append(parts, rt)
				}
			}
		}
	}
	return strings.Join(parts, " · ")
}

func formatCurrencyAmount(currency, amount string) string {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		amount = "0"
	}
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CNY", "RMB":
		return "¥" + amount
	case "USD":
		return "$" + amount
	case "EUR":
		return "€" + amount
	case "":
		return amount
	default:
		return strings.ToUpper(currency) + " " + amount
	}
}

func formatPlanBadge(plan string) string {
	p := strings.TrimSpace(plan)
	if p == "" {
		return ""
	}
	if len(p) <= 12 {
		return strings.ToUpper(p[:1]) + p[1:]
	}
	return p
}

func formatResetTime(resetAt, now time.Time) string {
	d := resetAt.Sub(now)
	if d <= 0 {
		return "即将重置"
	}
	return formatCountdown(d) + "后重置"
}

func formatCountdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSecs := int64(math.Ceil(d.Seconds()))
	if totalSecs < 60 {
		return fmt.Sprintf("%ds", totalSecs)
	}
	mins := totalSecs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	hours := mins / 60
	remMins := mins % 60
	if hours < 24 {
		if remMins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, remMins)
	}
	days := hours / 24
	remHours := hours % 24
	if remHours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, remHours)
}

func formatRelativeAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < 30*time.Second {
		return "刚刚"
	}
	mins := int(d / time.Minute)
	if mins < 60 {
		return fmt.Sprintf("%dm前", mins)
	}
	hours := mins / 60
	if hours < 24 {
		return fmt.Sprintf("%dh前", hours)
	}
	return fmt.Sprintf("%dd前", hours/24)
}

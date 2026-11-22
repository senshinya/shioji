package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

const modelFavoritePath = "M12 3l2.8 5.67 6.26.91-4.53 4.41 1.07 6.24L12 17.29l-5.6 2.94 1.07-6.24-4.53-4.41 6.26-.91Z"

var (
	modelFavoriteSymbol  = mobileSymbol(`<path d="` + modelFavoritePath + `"/>`)
	modelFavoritedSymbol = mobileSymbol(`<path fill="currentColor" d="` + modelFavoritePath + `"/>`)
)

func mobileModelName(model domain.Model) string {
	if model.Name != "" {
		return model.Name
	}
	return model.ID
}

func (a *mobileApp) agentModels(c *ui.Context, w *mygo.Window, deviceID string, agent domain.HarnessInfo, busy bool, refresh func()) {
	m := a.model
	pref := m.ModelPreference(deviceID, agent.ID)
	models := pref.Ordered(agent.Models, false)
	stale := agent.State == domain.RuntimeUnavailable || agent.State == domain.RuntimeRefreshing
	disabled := stale || busy || !m.Connected || deviceID == ""
	save := func(next app.ModelMenuPreferences) {
		if !a.pageCurrent(w) || a.model != m || m.Snapshot.DeviceID != deviceID {
			return
		}
		m.SetModelPreferences(deviceID, agent.ID, next)
		a.save()
		a.refresh()
	}

	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Column(c).Grow(1).Children(func() { mobileSection(c, "可用模型") })
		if stale && len(models) > 0 {
			oneLine(c, "上次读取", captionTextSize, palette(c).secondary)
		}
		action(c, "刷新模型", refresh).PaddingX(0).Disabled(busy || !m.Connected || m.WorkerMinor < 16 || a.preview)
	})
	if len(models) == 0 && !busy {
		mobileMessage(c, "尚未读取模型")
	}
	if stale && agent.Error != "" {
		mobileMessage(c, agent.Error)
	}
	for index, model := range models {
		row := c.Key(model.ID)
		ui.Row(row).MinHeight(max(contentRowHeight, mobileTapSize(c))).Gap(6).AlignItems(ui.Center).
			Transition(ui.ElementTransition{Position: true, Duration: mobileToolDisclosureDuration, Ease: ui.EaseOut}).
			Children(func() {
				name := mobileModelName(model)
				favorite := slices.Contains(pref.Favorites, model.ID)
				icon, favoriteLabel := modelFavoriteSymbol, "收藏 "+name
				if favorite {
					icon, favoriteLabel = modelFavoritedSymbol, "取消收藏 "+name
				}
				iconButton(row.Key("favorite"), favoriteLabel, icon, inlineIconSize, func() {
					next := pref
					next.Favorites = setMobileModelPreference(pref.Favorites, model.ID, !favorite)
					save(next)
				}).AccessSelected(favorite).Disabled(disabled)

				visible := !slices.Contains(pref.Hidden, model.ID)
				p := palette(c)
				ink := p.ink
				if !visible || stale {
					ink = p.secondary
				}
				ui.Column(row).Grow(1).MinWidth(0).Gap(2).Children(func() {
					label(row, name, bodyTextSize, ink)
					if model.Default {
						oneLine(row, "默认", captionTextSize, p.secondary)
					}
				})
				mobileSwitch(row.Key("visible"), "在模型菜单显示 "+name, &visible, func() {
					next := pref
					next.Hidden = setMobileModelPreference(pref.Hidden, model.ID, !visible)
					save(next)
				}).Disabled(disabled)
				if len(models) > 1 {
					move := func(to int) {
						next := pref
						order := make([]string, 0, len(models)+len(pref.Order))
						for _, entry := range models {
							order = append(order, entry.ID)
						}
						order[index], order[to] = order[to], order[index]
						for _, id := range pref.Order {
							if !slices.Contains(order, id) {
								order = append(order, id)
							}
						}
						next.Order = order
						save(next)
					}
					iconButton(row.Key("order"), name+" 模型操作", moreSymbol, inlineIconSize, nil).
						Disabled(disabled).PopoverMenuButton(func(menu *ui.Menu) {
						if menu.Item("上移").Disabled(index == 0).Chosen() {
							move(index - 1)
						}
						if menu.Item("下移").Disabled(index == len(models)-1).Chosen() {
							move(index + 1)
						}
					})
				}
			})
	}
}

func setMobileModelPreference(ids []string, id string, enabled bool) []string {
	out := slices.Clone(ids)
	if index := slices.Index(out, id); index >= 0 {
		if !enabled {
			return slices.Delete(out, index, index+1)
		}
	} else if enabled {
		return append(out, id)
	}
	return out
}

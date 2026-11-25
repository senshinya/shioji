package main

import (
	"errors"
	"slices"
	"strings"

	mygo "github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func mobileProfileModeLabel(mode domain.AgentProfileMode) string {
	if mode == domain.AgentProfileAPI {
		return "自定义 API"
	}
	return "账号授权"
}

func (a *mobileApp) openCreateAgentProfile(parent *mygo.Window, providers []domain.HarnessInfo, apply func(protocol.AgentSetupResult)) {
	request := mobileRequest{}
	provider := providers[0]
	mode := provider.ProfileModes[0]
	name := ""
	var sent domain.AgentProfile
	var w *mygo.Window
	w = a.page("新增 Profile", func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			formField(c, "名称", "Profile 名称", &name).Disabled(request.busy)
			formChoice(c, "Agent", provider.Name, func(menu *ui.Menu) {
				for _, item := range providers {
					menuChoice(menu, item.Name, item.ID == provider.ID, func() {
						provider = item
						if !slices.Contains(item.ProfileModes, mode) {
							mode = item.ProfileModes[0]
						}
					})
				}
			}).Disabled(request.busy)
			formChoice(c, "登录方式", mobileProfileModeLabel(mode), func(menu *ui.Menu) {
				for _, choice := range provider.ProfileModes {
					menuChoice(menu, mobileProfileModeLabel(choice), choice == mode, func() { mode = choice })
				}
			}).Disabled(request.busy || len(provider.ProfileModes) == 1)
		}, func() {
			request.status(c)
			primaryAction(c, "创建", func() {
				input := domain.AgentProfile{ID: sent.ID, Name: strings.TrimSpace(name), HarnessID: provider.ID, Mode: mode}
				if input.ID == "" || input != sent {
					input.ID = domain.NewID()
				}
				sent = input
				a.pageJob(w, &request, "runtime.profile.create", input, decodePage(func(result protocol.AgentSetupResult) error {
					if !result.Ready || !slices.ContainsFunc(result.Catalog, func(info domain.HarnessInfo) bool {
						return info.ID == input.ID && info.Profile != nil
					}) {
						return errors.New("设备未确认新建的 Profile")
					}
					apply(result)
					w.Pop()
					return nil
				}))
			}).Disabled(request.busy || !a.model.Connected || strings.TrimSpace(name) == "")
		})
	})
	a.pushNative(parent, w)
}

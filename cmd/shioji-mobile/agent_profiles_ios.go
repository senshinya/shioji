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
	var provider domain.HarnessInfo
	var mode domain.AgentProfileMode
	var selected, name, credential string
	var sent domain.AgentProfile
	var w *mygo.Window
	w = a.page("新增 Profile", func(c *ui.Context) {
		if selected != provider.ID {
			if index := slices.IndexFunc(providers, func(info domain.HarnessInfo) bool { return info.ID == selected }); index >= 0 {
				provider = providers[index]
				mode, name, credential = provider.ProfileModes[0], "", ""
			}
		}
		keyOnly := provider.Installation != nil && provider.Installation.APIKeyOnly
		mobilePageWithFooter(c, func() {
			ui.RadioGroup(c.Key("profile-agents"), func() {
				for _, item := range providers {
					p := palette(c)
					choice := ui.RadioBase(c.Key(item.ID), &selected, item.ID).Label(item.Name).
						Height(mobileTapSize(c)).PaddingX(8).Gap(4).Radius(5).AlignItems(ui.Center).Disabled(request.busy)
					if selected == item.ID {
						choice.Background(p.inset).Border(1, p.line)
					}
					choice.Children(func() {
						ui.Icon(c, mobileHarnessSymbol(item.Icon)).Size(14, 14).TextColor(p.secondary).PassThrough()
						oneLine(c, item.Name, 12, p.ink).PassThrough()
					})
				}
			}).Row().Wrap().FillWidth().Gap(4).Label("选择 Agent")
			if provider.ID == "" {
				return
			}
			if keyOnly {
				formRow(c, "API Key", func() {
					formInput(c.Key("profile-key:"+provider.ID), "API Key", "输入 API Key", &credential).
						Password().Autocorrect(false).Capitalize(ui.CapitalizeNone).Disabled(request.busy)
				})
				return
			}
			formField(c.Key("profile-name:"+provider.ID), "名称", "Profile 名称", &name).Disabled(request.busy)
			formChoice(c, "登录方式", mobileProfileModeLabel(mode), func(menu *ui.Menu) {
				for _, choice := range provider.ProfileModes {
					menuChoice(menu, mobileProfileModeLabel(choice), choice == mode, func() { mode = choice })
				}
			}).Disabled(request.busy || len(provider.ProfileModes) == 1)
		}, func() {
			request.status(c)
			if provider.ID == "" {
				return
			}
			caption := "创建"
			if keyOnly {
				caption = "创建并连接"
			}
			primaryAction(c, caption, func() {
				input := domain.AgentProfile{ID: sent.ID, Name: strings.TrimSpace(name), HarnessID: provider.ID, Mode: mode}
				if keyOnly {
					input.Name = provider.Name
				}
				if input.ID == "" || input != sent {
					input.ID = domain.NewID()
				}
				sent = input
				a.pageJob(w, &request, "runtime.profile.create", input, decodePage(func(result protocol.AgentSetupResult) error {
					index := slices.IndexFunc(result.Catalog, func(info domain.HarnessInfo) bool {
						return info.ID == input.ID && info.Profile != nil
					})
					if !result.Ready || index < 0 {
						return errors.New("设备未确认新建的 Profile")
					}
					apply(result)
					if keyOnly && !result.Catalog[index].Profile.Authorized {
						if !a.startAgentSignIn(result.Catalog[index], domain.SignInAPI, &domain.APIConfiguration{Credential: credential}) {
							return errors.New("未能发起 API 连接，请重试")
						}
						credential = ""
					}
					w.Pop()
					return nil
				}))
			}).Disabled(request.busy || !a.model.Connected || keyOnly && strings.TrimSpace(credential) == "" || !keyOnly && strings.TrimSpace(name) == "")
		})
	})
	a.pushNative(parent, w)
}

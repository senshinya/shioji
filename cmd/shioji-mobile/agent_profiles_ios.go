package main

import (
	"errors"
	"slices"
	"strings"
	"sync"

	mygo "github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/ui/assets"
)

const mobileCustomProfile = "custom-profile"

func mobileProfileModeLabel(mode domain.AgentProfileMode) string {
	if mode == domain.AgentProfileAPI {
		return "自定义 API"
	}
	return "账号授权"
}

func mobileCustomAPILabel(id string) string {
	if id == domain.AgentAnthropic {
		return "Anthropic API"
	}
	return "Responses API"
}

var mobileProfileMenuIcons = sync.OnceValue(func() map[string]*mygo.MenuIcon {
	icons := make(map[string]*mygo.MenuIcon, len(assets.ProfileIconChoices))
	for _, choice := range assets.ProfileIconChoices {
		icons[choice.ID], _ = mygo.NewMenuIconSVG(mobileHarnessSymbol(choice.ID), true)
	}
	return icons
})

func mobileProfileIcon(c *ui.Context, icon string, disabled bool, choose func(string)) {
	p := palette(c)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if picture := app.AgentProfileIconImage(icon); picture != nil {
			ui.Image(c, ui.NewBitmap(picture)).Size(20, 20).Fit(ui.Contain).PassThrough()
		} else {
			ui.Icon(c, mobileHarnessSymbol(icon)).Size(20, 20).TextColor(p.secondary).PassThrough()
		}
		iconAction(c, "选择图标", "选择图标", nil, 0, pickerSymbol, pickerIconSize, bodyTextSize, 400, nil).
			Disabled(disabled).Menu(func(menu *ui.Menu) {
			icons := mobileProfileMenuIcons()
			for _, choice := range assets.ProfileIconChoices {
				if menu.Item(choice.Name).Icon(icons[choice.ID]).Checked(choice.ID == icon).Chosen() {
					choose(choice.ID)
				}
			}
		})
	})
}

func profileFormChoice(c *ui.Context, title, value string, items func(*ui.Menu)) ui.Element {
	return plainFormRow(c, title, func() {
		formChoiceControl(c, title, value, items)
	})
}

func profileFormInput(c *ui.Context, title, placeholder string, value *string) ui.Element {
	return formInput(c, title, placeholder, value).PaddingX(8).Radius(5).Background(palette(c).inset)
}

func (a *mobileApp) openCreateAgentProfile(parent *mygo.Window, providers []domain.HarnessInfo, apply func(protocol.AgentSetupResult), initial ...domain.HarnessInfo) {
	request := mobileRequest{}
	modelRequest := mobileRequest{}
	customProviders := map[string]domain.HarnessInfo{}
	var choices []domain.HarnessInfo
	for _, provider := range providers {
		if provider.IsCustomProfileProvider() {
			customProviders[provider.ID] = provider
		} else if len(provider.ProfileModes) > 0 && provider.Installation != nil && provider.Installation.Issue != domain.RuntimeMissing && provider.Installation.Issue != domain.RuntimeIncompatible {
			choices = append(choices, provider)
		}
	}
	if len(customProviders) > 0 {
		choices = append(choices, domain.HarnessInfo{ID: mobileCustomProfile, Name: "自定义", Icon: "profile-sliders", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI}})
	}
	var provider domain.HarnessInfo
	var mode domain.AgentProfileMode
	var name, credential, icon, selectedAPI, completedRepairID string
	var sent domain.AgentProfile
	var saved domain.HarnessInfo
	pageTitle := "新增 Profile"
	if len(initial) > 0 && initial[0].Profile != nil && domain.IsCustomAgent(initial[0].Profile.HarnessID) {
		saved = initial[0]
		sent = *saved.Profile
		for _, choice := range choices {
			if choice.ID == mobileCustomProfile {
				provider = choice
				break
			}
		}
		if provider.ID == "" {
			provider = domain.HarnessInfo{ID: mobileCustomProfile, Name: "自定义"}
		}
		mode, name, icon, selectedAPI = sent.Mode, sent.Name, sent.Icon, sent.HarnessID
		pageTitle = sent.Name
		if previous := a.agentSignIns[saved.ID]; previous != nil && previous.status.Phase == domain.SignInCompleted {
			completedRepairID = previous.status.ID
		}
	}
	drafts := map[string]*domain.APIConfiguration{}
	apiDraft := func(id string) *domain.APIConfiguration {
		if drafts[id] == nil {
			drafts[id] = &domain.APIConfiguration{}
		}
		return drafts[id]
	}
	var models []domain.Model
	modelEndpoint, modelKey, modelAPI := "", "", ""
	var w *mygo.Window
	w = a.page(pageTitle, func(c *ui.Context) {
		custom := provider.ID == mobileCustomProfile
		keyOnly := provider.Installation != nil && provider.Installation.APIKeyOnly
		repair := saved.CanRepairAPIRuntime()
		attempt := a.agentSignIns[saved.ID]
		authBusy := custom && attempt != nil && attempt.status.Phase.Active()
		if custom && drafts != nil && attempt != nil && attempt.status.Phase == domain.SignInCompleted && attempt.status.Profile != nil &&
			attempt.status.Profile.Profile != nil && attempt.status.Profile.Profile.Authorized && (!repair || completedRepairID != attempt.status.ID) {
			drafts = nil
			mygo.RunOnMain(func() {
				if a.pageCurrent(w) {
					w.Pop()
				}
			})
			return
		}
		if drafts == nil {
			return
		}
		busy := request.busy || authBusy
		locked := busy || saved.ID != ""
		api := apiDraft(selectedAPI)
		installation := customProviders[selectedAPI].Installation
		if saved.Installation != nil {
			installation = saved.Installation
		}
		if installation != nil && attempt != nil && attempt.status.Setup != nil && attempt.status.Setup.Phase == "verifying" {
			next := *installation
			next.Issue = domain.RuntimeSignInRequired
			installation = &next
			saved.Installation = installation
		}
		missing := custom && (installation == nil || installation.Issue == domain.RuntimeMissing || installation.Issue == domain.RuntimeIncompatible)
		mobilePageWithFooter(c, func() {
			value := provider.Name
			if value == "" {
				value = "选择 Agent"
			}
			profileFormChoice(c, "Agent", value, func(menu *ui.Menu) {
				for _, item := range choices {
					menuChoice(menu, item.Name, item.ID == provider.ID, func() {
						provider = item
						mode, name, credential = item.ProfileModes[0], "", ""
						selectedAPI, icon = domain.AgentResponses, assets.DefaultProfileIcon
						if _, ok := customProviders[selectedAPI]; !ok {
							selectedAPI = domain.AgentAnthropic
						}
						drafts = map[string]*domain.APIConfiguration{}
						models = nil
						modelRequest.err = ""
					})
				}
			}).Disabled(locked)
			if provider.ID == "" {
				return
			}
			if keyOnly {
				plainFormRow(c, "API Key", func() {
					profileFormInput(c.Key("profile-key"), "API Key", "输入 API Key", &credential).
						Password().Autocorrect(false).Capitalize(ui.CapitalizeNone).Disabled(busy)
				})
				return
			}
			plainFormRow(c, "名称", func() {
				profileFormInput(c.Key("profile-name"), "名称", "Profile 名称", &name).Disabled(locked)
			})
			if custom {
				plainFormRow(c, "图标", func() {
					mobileProfileIcon(c, icon, locked, func(next string) {
						if saved.ID == "" {
							icon = next
						}
					})
				})
			}
			if !custom {
				ui.Box(c.Key("connection-gap")).Height(8).Shrink(0)
				profileFormChoice(c, "登录方式", mobileProfileModeLabel(mode), func(menu *ui.Menu) {
					for _, choice := range provider.ProfileModes {
						menuChoice(menu, mobileProfileModeLabel(choice), choice == mode, func() { mode = choice })
					}
				}).Disabled(locked || len(provider.ProfileModes) == 1)
				return
			}
			profileFormChoice(c, "API 类型", mobileCustomAPILabel(selectedAPI), func(menu *ui.Menu) {
				for _, id := range []string{domain.AgentResponses, domain.AgentAnthropic} {
					if _, ok := customProviders[id]; ok {
						menuChoice(menu, mobileCustomAPILabel(id), id == selectedAPI, func() { selectedAPI = id; models = nil; modelRequest.err = "" })
					}
				}
			}).Disabled(locked)
			if repair {
				plainFormRow(c, "运行环境", func() { oneLine(c, "Claude Code · 未安装", bodyTextSize, palette(c).secondary) })
				return
			}
			placeholder := "https://api.example.com/v1"
			if selectedAPI == domain.AgentAnthropic {
				placeholder = "https://api.anthropic.com"
			}
			plainFormRow(c, "API 地址", func() {
				profileFormInput(c.Key("endpoint:"+selectedAPI), "API 地址", placeholder, &api.Endpoint).Autocorrect(false).Capitalize(ui.CapitalizeNone).Disabled(busy)
			})
			plainFormRow(c, "API Key", func() {
				profileFormInput(c.Key("key:"+selectedAPI), "API Key", "输入 API Key", &api.Credential).Password().Autocorrect(false).Capitalize(ui.CapitalizeNone).Disabled(busy)
			})
			plainFormRow(c, "模型 ID", func() {
				profileFormInput(c.Key("model:"+selectedAPI), "模型 ID", "输入模型 ID", &api.Model).Disabled(busy)
			})
			if len(models) > 0 && selectedAPI == modelAPI && api.Endpoint == modelEndpoint && api.Credential == modelKey {
				profileFormChoice(c, "模型列表", "选择模型", func(menu *ui.Menu) {
					for _, model := range models {
						menuChoice(menu, model.ID, model.ID == api.Model, func() { api.Model = model.ID; models = nil })
					}
				}).Disabled(busy)
			} else {
				action(c, "获取模型", func() {
					modelAPI, modelEndpoint, modelKey = selectedAPI, api.Endpoint, api.Credential
					input := protocol.ProfileModelInput{HarnessID: modelAPI, API: domain.APIConfiguration{Endpoint: modelEndpoint, Credential: modelKey}}
					a.pageJob(w, &modelRequest, "runtime.profile.models", input, decodePage(func(out []domain.Model) error { models = out; return nil }))
				}).AlignSelf(ui.End).Disabled(busy || modelRequest.busy || !a.model.Connected || strings.TrimSpace(api.Endpoint) == "" || strings.TrimSpace(api.Credential) == "")
			}
			modelRequest.status(c)
			engine := "Codex"
			if selectedAPI == domain.AgentAnthropic {
				engine = "Claude Code"
			}
			status := "已就绪"
			if missing {
				status = "未安装"
			}
			plainFormRow(c, "运行环境", func() { oneLine(c, engine+" · "+status, bodyTextSize, palette(c).secondary) })
		}, func() {
			request.status(c)
			if provider.ID == "" {
				return
			}
			caption := "创建"
			if keyOnly || custom {
				caption = "创建并连接"
			}
			if missing {
				caption = "安装并连接"
			}
			if custom && saved.ID != "" && !missing {
				caption = "重试连接"
			}
			if repair {
				caption = "安装运行环境"
			}
			if authBusy {
				message := "正在准备运行环境"
				if setup := attempt.status.Setup; setup != nil {
					if setup.Phase == "verifying" {
						message = "正在验证 API 连接"
					}
					if setup.Phase == "downloading" && setup.Total > 0 {
						message = "正在下载运行环境"
						ui.Progress(c, float64(setup.Downloaded)/float64(setup.Total)).FillWidth()
					}
				}
				mobileMessage(c, message)
				action(c, "取消", func() {
					a.sendAgentSignIn(attempt, "cancel", protocol.SignInInput{ID: attempt.status.ID, HarnessID: saved.ID})
				}).Disabled(attempt.pending)
			} else if custom && attempt != nil {
				if attempt.message != "" {
					mobileMessage(c, attempt.message)
				} else if attempt.status.Message != "" {
					mobileMessage(c, attempt.status.Message)
				}
			}
			primaryAction(c, caption, func() {
				if repair {
					if !a.startAgentSignIn(saved, domain.SignInAPI, &domain.APIConfiguration{InstallRuntime: true}) {
						request.err = "未能开始安装，请重试"
					}
					return
				}
				connect := func(info domain.HarnessInfo) error {
					configuration := domain.APIConfiguration{Credential: credential}
					if custom {
						configuration = *api
						configuration.InstallRuntime = missing
					}
					if !a.startAgentSignIn(info, domain.SignInAPI, &configuration) {
						return errors.New("未能发起 API 连接，请重试")
					}
					return nil
				}
				if custom && saved.ID != "" {
					if err := connect(saved); err != nil {
						request.err = err.Error()
					}
					return
				}
				input := domain.AgentProfile{ID: sent.ID, Name: strings.TrimSpace(name), HarnessID: provider.ID, Mode: mode}
				if keyOnly {
					input.Name = provider.Name
				}
				if custom {
					input.HarnessID, input.Icon = selectedAPI, icon
				}
				if input.ID == "" || input != sent {
					input.ID = domain.NewID()
				}
				sent = input
				a.pageJob(w, &request, "runtime.profile.create", input, decodePage(func(result protocol.AgentSetupResult) error {
					index := slices.IndexFunc(result.Catalog, func(info domain.HarnessInfo) bool { return info.ID == input.ID && info.Profile != nil })
					if !result.Ready || index < 0 {
						return errors.New("设备未确认新建的 Profile")
					}
					apply(result)
					newProfile := result.Catalog[index]
					if custom {
						saved = newProfile
						return connect(newProfile)
					}
					if keyOnly && !newProfile.Profile.Authorized {
						if err := connect(newProfile); err != nil {
							return err
						}
						credential = ""
					}
					w.Pop()
					return nil
				}))
			}).Disabled(busy || !a.model.Connected || keyOnly && strings.TrimSpace(credential) == "" ||
				!keyOnly && strings.TrimSpace(name) == "" || custom && !repair &&
				(strings.TrimSpace(api.Endpoint) == "" || strings.TrimSpace(api.Credential) == "" || strings.TrimSpace(api.Model) == ""))
		})
	})
	a.pushNative(parent, w)
}

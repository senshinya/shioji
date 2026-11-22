package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func (a *mobileApp) settingsView(c *ui.Context) {
	version := mygo.App.Version()
	if version == "" {
		version = "开发版"
	}
	root := ui.Column(c).Fill().Background(palette(c).canvas)
	scroll := ui.Local(root, "settings-scroll", func() ui.ScrollState { return ui.ScrollState{} })
	resetScroll := func() { scroll.ScrollTo(0, 0, ui.Animation{}); a.settings.Invalidate() }
	root.Children(func() {
		mobilePageContent(c, 0).Shrink(0).Children(func() {
			key := c.Key("settings-search")
			mobileSearchField(key, ui.Row(key), &a.settingsQuery, "搜索设置", "搜索设置", func() {
				if a.settingsQuery != "" {
					iconButton(c.Key("clear"), "清除设置搜索", closeSymbol, 13, func() {
						a.settingsQuery = ""
						resetScroll()
					}).KeepFocus().Margin(0, -9, 0, 0)
				}
			}).OnChange(resetScroll)
		})
		mobilePageBody(c.Key("settings-content"), func() {
			if query := strings.Fields(strings.ToLower(a.settingsQuery)); len(query) != 0 {
				a.settingsResults(c, query, version)
				return
			}
			mobileSection(c, "偏好")
			mobileRow(c.Key("appearance"), settingsAppearance, "", "", appearanceSymbol, func() { a.openInputSettings(a.settings, "") })
			mobileRow(c.Key("notifications"), "通知偏好", "", "", infoSymbol, func() { a.openNotificationSettings(a.settings, "") })
			mobileRow(c.Key("shortcuts"), settingsShortcuts, "", "", keyboardSymbol, func() { a.openKeyboardSettings(a.settings, "") })
			mobileGroupHeading(c, settingsAbout)
			mobileRow(c.Key("system"), "外观", "", appearanceName(a.model.Preferences.Appearance), appearanceSymbol, nil)
			mobileRow(c.Key("version"), "版本", "", version, infoSymbol, nil)
			mobileRow(c.Key("licenses"), "第三方许可", "", "", filesSymbol, func() { a.openThirdPartyLicenses(a.settings) })
		}).Grow(1).Basis(0).MinHeight(0).TrackScroll(scroll)
	})
}

func appearanceName(appearance string) string {
	switch appearance {
	case "light":
		return "浅色"
	case "dark":
		return "深色"
	default:
		return "跟随系统"
	}
}

func (a *mobileApp) openInputSettings(parent *mygo.Window, target string) {
	var shortcutError string
	var showPreview bool
	commit := func() {
		a.save()
		a.refresh()
	}
	var w *mygo.Window
	w = a.page(settingsAppearance, func(c *ui.Context) {
		mobilePageBody(c, func() {
			prefs := &a.model.Preferences
			showTools := !prefs.CollapseLatestTools
			mobileSection(c, "外观")
			revealSetting(&target, "theme", formChoice(c.Key("theme"), "主题", appearanceName(prefs.Appearance), func(m *ui.Menu) {
				for _, theme := range []string{"system", "light", "dark"} {
					menuChoice(m, appearanceName(theme), prefs.Appearance == theme, func() {
						prefs.Appearance = theme
						mygo.Theme.SetSource(mygo.ThemeSource(theme))
						commit()
					})
				}
			}))
			revealSetting(&target, "interface-zoom", a.interfaceZoom(c.Key("interface-zoom")))
			revealSetting(&target, "interface-font", formRow(c.Key("interface-font"), "界面字体", func() {
				name := interfaceFontName(prefs.Normalized().InterfaceFont)
				iconAction(c, name, "界面字体", nil, 0, rightSymbol, pickerIconSize, bodyTextSize, 400,
					func() { a.openInterfaceFont(w, commit) }).
					PaddingX(0).MaxWidthPercent(62).Value(name)
			}))
			revealSetting(&target, "reply-size", formChoice(c.Key("reply-size"), "回复字号", fmt.Sprint(prefs.ReplySize)+" pt", func(m *ui.Menu) {
				for size := 12; size <= 20; size++ {
					size := size
					menuChoice(m, fmt.Sprint(size)+" pt", size == prefs.ReplySize, func() {
						prefs.ReplySize = size
						commit()
					})
				}
			}))
			fontName := prefs.Normalized().CodeFont
			if fontName == "SF Mono" {
				fontName = "系统等宽字体"
			}
			revealSetting(&target, "code-font", formChoice(c.Key("code-font"), "代码字体", fontName, func(m *ui.Menu) {
				choose := func(name, family string) {
					menuChoice(m, name, prefs.Normalized().CodeFont == family, func() {
						prefs.CodeFont = family
						commit()
					})
				}
				choose("系统等宽字体", "SF Mono")
				for _, family := range ui.FontFamilies() {
					if !family.Registered && family.Monospaced && family.Name != "SF Mono" {
						choose(family.Name, family.Name)
					}
				}
			}))
			motion := "跟随系统"
			if prefs.ReduceMotion {
				motion = "开启"
			}
			revealSetting(&target, "motion", formChoice(c.Key("motion"), "减少动态效果", motion, func(m *ui.Menu) {
				menuChoice(m, "跟随系统", !prefs.ReduceMotion, func() {
					prefs.ReduceMotion = false
					commit()
				})
				menuChoice(m, "开启", prefs.ReduceMotion, func() {
					prefs.ReduceMotion = true
					commit()
				})
			}))
			revealSetting(&target, "appearance-preview", a.appearancePreview(c, w, &showPreview))
			mobileGroupHeading(c, "会话")
			revealSetting(&target, "history-rounds", formChoice(c.Key("history-rounds"), "每次加载轮次", fmt.Sprint(prefs.HistoryPageRounds), func(m *ui.Menu) {
				for _, rounds := range []int{5, 10, 20, 50, 100} {
					menuChoice(m, fmt.Sprint(rounds), rounds == prefs.HistoryPageRounds, func() {
						prefs.HistoryPageRounds = rounds
						a.save()
					})
				}
			}))
			revealSetting(&target, "tools", mobileToggle(c.Key("tools"), "自动展开最新工具调用", &showTools, func() {
				prefs.CollapseLatestTools = !showTools
				a.save()
				a.refresh()
			}))
			mobileGroupHeading(c, "硬件键盘")
			bindings := prefs.MobileShortcuts()
			for _, item := range []struct{ id, title, binding string }{
				{"send", "发送或加入队列", bindings.Send},
				{"newline", "换行", bindings.Newline},
				{"steer", "立即介入当前轮", bindings.Steer},
			} {
				revealSetting(&target, item.id, formChoice(c.Key(item.id), item.title, mobileBindingLabel(item.binding), func(menu *ui.Menu) {
					for _, binding := range app.ComposerBindings {
						menuChoice(menu, mobileBindingLabel(binding), item.binding == binding, func() {
							if err := a.model.Preferences.SetMobileShortcut(item.id, binding); err != nil {
								shortcutError = err.Error()
								w.Invalidate()
								return
							}
							shortcutError = ""
							a.save()
							a.refresh()
						})
					}
				}))
			}
			message := shortcutError
			if a.draftSaveFailed() {
				message = "偏好尚未保存"
			}
			if message != "" {
				label(c, message, captionTextSize, palette(c).warning).Role(ui.RoleStatus).Padding(6, 0, 0)
			}
			revealSetting(&target, "restore-shortcuts", action(c.Key("restore-shortcuts"), "恢复默认按键", func() {
				a.model.Preferences.ResetMobileShortcuts()
				shortcutError = ""
				a.save()
				a.refresh()
			}).AlignSelf(ui.Start).PaddingX(0))
			label(c, "软件键盘的 Return 用于换行。", captionTextSize, palette(c).secondary)
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) appearancePreview(c *ui.Context, owner *mygo.Window, expanded *bool) ui.Element {
	p := palette(c)
	toggle := c.Key("appearance-preview")
	row := mobileButton(toggle, "对话预览", func() { *expanded = !*expanded }).
		FillWidth().MinHeight(max(formRowHeight, mobileTapSize(c))).AlignItems(ui.Center).Gap(8).Expanded(*expanded)
	rowDivider(row, p.line).Children(func() {
		label(toggle, "对话预览", bodyTextSize, p.ink).Grow(1).PassThrough()
		arrow := rightSymbol
		if *expanded {
			arrow = downSymbol
		}
		ui.Icon(toggle, arrow).Size(pickerIconSize, pickerIconSize).TextColor(p.secondary).PassThrough()
	})
	mobileDisclosureBody(c.Key("appearance-sample"), *expanded, func(c *ui.Context) {
		const sample = "Shioji 已保存改动。下次运行会采用新配置。\n\n```go\nfmt.Println(\"Shioji\")\n```"
		ui.Column(c).Padding(10, 0, 4).Children(func() {
			a.drawTranscriptMarkdown(c, owner, domain.Session{}, float32(a.model.Preferences.ReplySize), nil,
				func(body *ui.Context, options *markdown.Options) { markdown.View(body, sample, options).Selectable() })
		})
	})
	return row
}

func mobileBindingLabel(binding string) string {
	return strings.NewReplacer("Cmd+", "⌘ ", "Shift+", "⇧ ", "Ctrl+", "⌃ ", "Alt+", "⌥ ", "Enter", "Return").Replace(binding)
}

func (a *mobileApp) openNotificationSettings(parent *mygo.Window, target string) {
	ctx, cancel := context.WithCancel(context.Background())
	var w *mygo.Window
	var authorization, message string
	var authorizing, sending bool
	call := func(method string, result any, done func(error)) {
		go func() {
			err := uikit.Call(ctx, method, nil, result)
			mygo.RunOnMain(func() {
				if ctx.Err() == nil {
					done(err)
				}
			})
		}()
	}
	checkAuthorization := func() {
		if authorizing {
			return
		}
		var result struct{ Authorization string }
		call("notificationSettings", &result, func(err error) {
			if err == nil && !authorizing {
				if authorization == "allowed" && result.Authorization != "allowed" {
					message = ""
				}
				authorization = result.Authorization
				w.Invalidate()
			}
		})
	}
	update := func() { a.save(); a.refresh() }
	authorize := func() {
		authorizing, message = true, ""
		w.Invalidate()
		// The system prompt stays open until the user answers.
		call("authorizeNotifications", nil, func(err error) {
			authorizing = false
			if err == nil {
				authorization = "allowed"
				a.model.Preferences.Notifications.Enabled = true
				update()
			} else {
				var failure *mygo.NativeError
				if errors.As(err, &failure) && failure.Code == "NotificationsDenied" {
					authorization = "denied"
				} else {
					message = "无法请求通知授权"
				}
			}
			w.Invalidate()
		})
	}
	sendTest := func() {
		sending, message = true, ""
		w.Invalidate()
		notice := mygo.NewNotification(mygo.NotificationOptions{
			ID: "shioji-settings-test", Title: "Shioji", Body: "通知已准备就绪", Silent: !a.model.Preferences.Notifications.Sound,
		})
		go func() {
			err := notice.ShowContext(ctx)
			mygo.RunOnMain(func() {
				if ctx.Err() != nil {
					return
				}
				sending = false
				if err == nil {
					message = "测试通知已发送"
				} else {
					message = "无法发送测试通知"
					checkAuthorization()
				}
				w.Invalidate()
			})
		}()
	}
	w = a.page(settingsNotices, func(c *ui.Context) {
		mobilePageBody(c, func() {
			prefs := a.model.Preferences.Notifications
			mobileSection(c, "系统通知")
			enabled := prefs.Enabled || authorizing
			revealSetting(&target, "allow", formRow(c.Key("allow"), "允许通知", func() {
				mobileSwitch(c, "允许通知", &enabled, func() {
					if enabled {
						authorize()
					} else {
						prefs.Enabled = false
						message = ""
						update()
					}
				}).Disabled(authorizing || sending)
			}))
			revealSetting(&target, "foreground", mobileToggle(c.Key("foreground"), "前台也通知", &prefs.Foreground, update))
			revealSetting(&target, "sound", mobileToggle(c.Key("sound"), "提示音", &prefs.Sound, update))
			mobileGroupHeading(c, "事件")
			revealSetting(&target, "completed", mobileToggle(c.Key("completed"), "回复完成", &prefs.Completed, update))
			revealSetting(&target, "attention", mobileToggle(c.Key("attention"), "需要回答或授权", &prefs.Attention, update))
			revealSetting(&target, "failed", mobileToggle(c.Key("failed"), "执行失败", &prefs.Failed, update))
			ui.Column(c.Key("actions")).FillWidth().Margin(12, 0, 0).Children(func() {
				const testTitle, settingsTitle = "发送测试通知", "打开系统通知设置"
				test := c.Key("test-notice")
				row := mobileButton(test, testTitle, sendTest).Disabled(!prefs.Enabled || authorization != "allowed" || authorizing || sending)
				mobileRowContent(test, row, testTitle, "", infoSymbol, false, nil)
				revealSetting(&target, "test-notice", row)
				revealSetting(&target, "system-settings", mobileRow(c.Key("system-settings"), settingsTitle, "", "", gearSymbol, func() {
					call("openNotificationSettings", nil, func(err error) {
						if err != nil {
							message = "无法打开系统通知设置"
							w.Invalidate()
						}
					})
				}))
			})
			status := message
			switch {
			case a.draftSaveFailed():
				status = "偏好尚未保存"
			case authorizing:
				status = "等待系统授权…"
			case sending:
				status = "正在发送测试通知…"
			case authorization == "denied" && (message == "" || message == "无法发送测试通知"):
				status = "请在系统设置中允许 Shioji 通知"
			}
			if status != "" {
				label(c.Key("status"), status, controlTextSize, palette(c).secondary).Role(ui.RoleStatus).Padding(8, 0, 0)
			}
		})
	})
	off := mygo.App.OnDidBecomeActive(checkAuthorization)
	w.OnClosed(func() { off(); cancel() })
	a.pushNative(parent, w)
	checkAuthorization()
}

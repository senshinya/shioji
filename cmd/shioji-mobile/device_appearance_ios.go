package main

import (
	"errors"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/mobileclient"
)

var mobileDeviceIcons = []struct {
	id   app.DeviceIcon
	name string
	icon *ui.SVG
}{
	{app.DeviceIconAuto, "自动", deviceSymbol},
	{app.DeviceIconMonitor, "显示器", deviceSymbol},
	{app.DeviceIconLaptop, "笔记本", mobileSymbol(`<rect x="4" y="4" width="16" height="12" rx="2"/><path d="m4 16-2 4h20l-2-4M10 17h4"/>`)},
	{app.DeviceIconTablet, "平板", mobileSymbol(`<rect x="2" y="5" width="20" height="14" rx="2"/><path d="M6 11v2"/>`)},
	{app.DeviceIconPhone, "手机", mobileSymbol(`<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 5h2m-2 14h2"/>`)},
	{app.DeviceIconServer, "服务器", serverSymbol},
}

func mobileDeviceIcon(icon app.DeviceIcon) (string, *ui.SVG) {
	for _, choice := range mobileDeviceIcons {
		if choice.id == icon {
			return choice.name, choice.icon
		}
	}
	return mobileDeviceIcons[0].name, mobileDeviceIcons[0].icon
}

func (a *mobileApp) deviceLabel(id, address string) string {
	return mobileclient.DeviceLabel(a.splitRoot().savedDevice(id).Name, address)
}

func (a *mobileApp) openDeviceAppearance(parent *mygo.Window, deviceID string) {
	root := a.splitRoot()
	entry := root.savedDevice(deviceID)
	if entry.Endpoint.DeviceID != deviceID || deviceID == "" {
		return
	}
	name, icon := entry.Name, entry.Icon
	if !icon.Valid() {
		icon = app.DeviceIconAuto
	}
	var w *mygo.Window
	var message string
	var composing bool
	save := func() {
		if composing || a.endpoint.DeviceID != deviceID || !a.pageCurrent(parent) || !a.pageCurrent(w) {
			return
		}
		if err := root.saveDeviceAppearance(deviceID, name, icon); err != nil {
			message = "无法保存设备外观 · " + err.Error()
			w.Invalidate()
			return
		}
		parent.SetTitle(a.deviceName())
		root.updateChatHeader()
		root.refresh()
		w.Pop()
	}
	w = a.page("编辑设备", func(c *ui.Context) {
		mobilePageBody(c, func() {
			formRow(c.Key("device-name"), "显示名称", func() {
				input := formInput(c, "设备显示名称", deviceHost(entry.Endpoint), &name)
				composing = input.Composing()
				if input.Submitted() && !composing {
					save()
				}
			})
			iconName, glyph := mobileDeviceIcon(icon)
			formRow(c.Key("device-icon"), "设备图标", func() {
				iconAction(c, iconName, "设备图标", glyph, inlineIconSize, pickerSymbol, pickerIconSize, bodyTextSize, 400, nil).
					PaddingX(0).MaxWidthPercent(62).Value(iconName).PopoverMenuButton(func(m *ui.Menu) {
					for _, choice := range mobileDeviceIcons {
						menuChoice(m, choice.name, choice.id == icon, func() { icon = choice.id })
					}
				})
			})
			if message != "" {
				label(c, message, captionTextSize, palette(c).warning).Role(ui.RoleStatus).Padding(6, 0, 0)
			}
		})
	})
	a.pushWithCommit(parent, w, "保存设备外观", save)
}

func (a *mobileApp) saveDeviceAppearance(deviceID, name string, icon app.DeviceIcon) error {
	next := a.savedDevices
	for i, device := range next.Devices {
		if device.Endpoint.DeviceID != deviceID {
			continue
		}
		updated, err := device.WithAppearance(name, icon)
		if err != nil {
			return err
		}
		if updated.Name == device.Name && updated.Icon == device.Icon {
			return nil
		}
		next.Devices = slices.Clone(next.Devices)
		next.Devices[i] = updated
		if a.preview {
			a.savedDevices = next
			return nil
		}
		return a.saveDeviceRegistry(next)
	}
	return errors.New("该设备已移除")
}

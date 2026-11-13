package ui

import (
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"time"

	n "github.com/egoist/mygo/ui"
)

var laptopDeviceIcon = svg("M4 3h12v10H4zM2 14h16l-1.5 3h-13z")
var tabletDeviceIcon = svg("M5 3h10v14H5zM9 15.5h2")
var phoneDeviceIcon = svg("M6 3h8v14H6zM9 15.5h2")
var serverDeviceIcon = svg("M3 3h14v6H3zM3 11h14v6H3zM6 6h.1M6 14h.1")

func deviceGlyph(icon app.DeviceIcon) *n.SVG {
	switch icon {
	case app.DeviceIconLaptop:
		return laptopDeviceIcon
	case app.DeviceIconTablet:
		return tabletDeviceIcon
	case app.DeviceIconPhone:
		return phoneDeviceIcon
	case app.DeviceIconServer:
		return serverDeviceIcon
	default:
		return monitor
	}
}

func (v *View) peerDevice(p tailnet.Peer) (app.DeviceConnection, bool) {
	for _, connection := range v.Model.Connections {
		for _, route := range connection.Normalized().Routes {
			if route.SSH.TailnetPeer == p.ID && p.ID != "" {
				return connection, true
			}
		}
	}
	return app.DeviceConnection{}, false
}

func (v *View) tailnetPeerName(p tailnet.Peer) string {
	if connection, ok := v.peerDevice(p); ok && connection.Name != "" {
		return connection.Name
	}
	return p.Name
}

func (v *View) tailnetPeerGlyph(p tailnet.Peer) *n.SVG {
	if connection, ok := v.peerDevice(p); ok && connection.Icon != app.DeviceIconAuto {
		return deviceGlyph(connection.Icon)
	}
	return deviceGlyph(app.DefaultDeviceIcon(p.OS))
}

func (v *View) deviceDefaultIcon(key string) app.DeviceIcon {
	if key == "" && v.sshTailnetPeer != "" {
		return app.DefaultDeviceIcon(v.tailnetPathPeer(v.sshTailnetPeer).OS)
	}
	for _, connection := range v.Model.Connections {
		if connection.ID == key {
			for _, route := range connection.Normalized().Routes {
				if route.SSH.TailnetPeer != "" {
					return app.DefaultDeviceIcon(v.tailnetPathPeer(route.SSH.TailnetPeer).OS)
				}
			}
		}
	}
	return app.DeviceIconMonitor
}

func (v *View) deviceGlyph(key string) *n.SVG {
	icon := v.Model.DeviceIconChoice(key)
	if icon == app.DeviceIconAuto {
		icon = v.deviceDefaultIcon(key)
	}
	return deviceGlyph(icon)
}

func (v *View) deviceIconPicker(c *n.Context, t *n.Theme, key, label, reference string, font n.Font, selected, automatic app.DeviceIcon, choose func(app.DeviceIcon)) n.Element {
	icon := selected
	if icon == app.DeviceIconAuto {
		icon = automatic
	}
	trigger := control(c.Key("icon:"+key)).Size(28, 28).Label(label).Children(func() {
		inlineSymbol(c.Key(string(icon)), deviceGlyph(icon), reference, font, 16, t.TextMuted).Transition(fadeMotion(c, 120*time.Millisecond))
	})
	if v.menuOpen == nil {
		v.menuOpen = map[string]bool{}
	}
	menuKey := "device-icon:" + key
	open := v.menuOpen[menuKey]
	if trigger.Clicked() {
		open = !open
	}
	popover(c.Key(menuKey), trigger, &open, func(panel n.Element) {
		panel.Padding(4).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.Row(c).Gap(4).FocusGroup(n.Horizontal).Children(func() {
				for _, item := range []struct {
					kind app.DeviceIcon
					name string
				}{{app.DeviceIconMonitor, "显示器"}, {app.DeviceIconLaptop, "笔记本"}, {app.DeviceIconTablet, "平板"}, {app.DeviceIconPhone, "手机"}, {app.DeviceIconServer, "服务器"}} {
					button := control(c.Key(string(item.kind))).Size(28, 28).Radius(5).Label(item.name)
					if item.kind == icon {
						button.Background(menuSelectionSurface(t)).AutoFocus()
					}
					button.Children(func() { n.Icon(c, deviceGlyph(item.kind)).Size(16, 16).TextColor(t.TextMuted) })
					if button.Clicked() {
						choose(item.kind)
						open = false
						trigger.Focus()
					}
				}
			})
		})
	})
	v.menuOpen[menuKey] = open
	return trigger
}

func (v *View) welcomeDeviceIcon(c *n.Context, t *n.Theme, key, label string, showName bool) {
	f := v.setupFlow()
	selected, known := f.icon, key == "local"
	for _, connection := range v.Model.Connections {
		if connection.ID == key {
			known = true
		}
	}
	if known {
		selected = v.Model.DeviceIconChoice(key)
	}
	name := v.welcomeDeviceName(key)
	n.Row(c).Gap(4).AlignItems(n.Baseline).Children(func() {
		v.deviceIconPicker(c, t, "welcome:"+label+":"+key, label, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, selected, v.deviceDefaultIcon(key), func(icon app.DeviceIcon) {
			if known {
				if err := v.Model.SetDeviceIcon(key, icon); err != nil {
					f.error = err.Error()
				}
			} else {
				f.icon = icon
			}
		})
		if showName {
			n.Text(c, name).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MaxWidth(240).Tooltip(name)
		}
	})
}

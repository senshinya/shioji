package ui

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/portforward"
)

func (v *View) forwardLinkMenu(c *n.Context, session domain.Session) func(*n.Menu, string) {
	connection := v.Model.WorkspaceConnection(session.WorkspaceID)
	device := v.Model.ConnectionDeviceID(connection)
	if session.ID == "" || session.Context.DeviceID != "" && session.Context.DeviceID != device {
		return nil
	}
	return func(menu *n.Menu, dest string) {
		if _, err := portforward.URLTarget(dest); err == nil && connection != "" && connection != "local" && device != "" {
			if menu.Item("端口转发并打开").Disabled(v.Controller == nil).Chosen() {
				v.openForwardedLink(session, dest)
			}
			menu.Separator()
		}
		address, _ := url.Parse(dest)
		web := address != nil && (address.Scheme == "http" || address.Scheme == "https") && address.Hostname() != "" && address.User == nil
		path, fileErr := fileLinkPath(dest)
		file := fileErr == nil && !n.SafeToOpen(dest) && !strings.HasPrefix(dest, "#")
		if web || file || n.SafeToOpen(dest) {
			if menu.Item("用默认应用打开").Disabled(file && (!defaultFileApplication(path) || v.Controller == nil || !v.Model.Connected)).Chosen() {
				if file {
					v.startLinkResource(session, path, c.Services(), fileLinkOpen)
				} else if _, err := portforward.URLTarget(dest); err == nil && connection != "" && connection != "local" {
					v.openForwardedLink(session, dest)
				} else {
					c.OpenURLThen(dest, func(err error) {
						if err != nil {
							v.persistenceModel().Error = err.Error()
						}
					})
				}
			}
		}
		if web || file {
			if menu.Item("另存为…").Disabled(v.Controller == nil || !v.Model.Connected).Chosen() {
				resource := dest
				if file {
					resource = path
				}
				v.startLinkResource(session, resource, c.Services(), fileLinkSave)
			}
		}
		if menu.Item("复制链接").Chosen() {
			c.WriteClipboard(dest)
		}
		menu.EditItems()
	}
}

func (v *View) openForwardedLink(session domain.Session, dest string) {
	connection := v.Model.WorkspaceConnection(session.WorkspaceID)
	device := v.Model.ConnectionDeviceID(connection)
	target, err := portforward.URLTarget(dest)
	if err != nil || v.Controller == nil {
		return
	}
	input := app.ForwardInput{Request: portforward.Request{DeviceID: device, ConnectionID: connection, Name: v.Model.DeviceName(device), Target: target, LocalPort: target.Port, AutoPort: true}, URL: dest, OpenURL: openForwardURL}
	if !v.Controller.Do(app.Job{ConnectionID: connection, Method: "$forward.create", Input: input, Apply: func(m *app.Model, body json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
			return
		}
		var item portforward.Mapping
		if json.Unmarshal(body, &item) == nil {
			v.revealForward(item.ID)
		}
	}}) {
		v.persistenceModel().Error = "操作繁忙，请重试"
	}
}

var messageURL = regexp.MustCompile(`(?i)(?:https?://|file:///)[^\s<>"']+`)

// Plain user text keeps its native selection and inline reference objects;
// only actual URL runs become native links, without parsing Markdown syntax.
func (v *View) userMessageLinks(c *n.Context, text string) {
	menu := v.forwardLinkMenu(c, v.Model.Details[v.Model.SessionID].Session)
	at := 0
	for _, span := range messageURL.FindAllStringIndex(text, -1) {
		start, end := span[0], span[1]
		raw := strings.TrimRight(text[start:end], ".,!?;，。！？；、）】")
		for strings.HasSuffix(raw, ")") && strings.Count(raw, ")") > strings.Count(raw, "(") {
			raw = strings.TrimSuffix(raw, ")")
		}
		end = start + len(raw)
		n.Text(c, text[at:start])
		link := n.Link(c, raw, raw).Underline().TextColor(c.Theme().Text).Tooltip(raw)
		if menu != nil {
			link.ContextMenu(func(m *n.Menu) { menu(m, raw) })
		}
		at = end
	}
	if at < len(text) {
		n.Text(c, text[at:])
	}
}

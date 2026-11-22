package main

import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/portforward"
)

var mobileMessageURL = regexp.MustCompile(`(?i)(?:https?://|file:///)[^\s<>"']+`)

func (a *mobileApp) messageTextLinks(c *ui.Context, parent *mygo.Window, source domain.Session, text string) {
	offset := 0
	for _, indices := range mobileMessageURL.FindAllStringIndex(text, -1) {
		start, end := indices[0], indices[1]
		dest := strings.TrimRight(text[start:end], ".,!?;，。！？；、）】")
		for strings.HasSuffix(dest, ")") && strings.Count(dest, ")") > strings.Count(dest, "(") {
			dest = strings.TrimSuffix(dest, ")")
		}
		if !ui.SafeToOpen(dest) {
			if _, _, err := mobileFileLinkTarget("", dest); err != nil {
				continue
			}
		}
		part := c.Key(start)
		if start != offset {
			ui.RichText(part, ui.Span{Text: text[offset:start]})
		}
		link := ui.Link(part.Key("link"), "", "").Label(dest).Underline()
		link.Children(func() { ui.RichText(part, ui.Span{Text: dest}) })
		link.ContextMenu(func(menu *ui.Menu) { a.conversationLinkMenu(menu, parent, source, "", dest) })
		if link.Clicked() {
			a.activateConversationLink(parent, source, "", dest)
		}
		offset = start + len(dest)
	}
	if offset < len(text) {
		ui.RichText(c, ui.Span{Text: text[offset:]})
	}
}

func (a *mobileApp) conversationLinkMenu(menu *ui.Menu, parent *mygo.Window, source domain.Session, sourcePath, dest string) {
	var filePath string
	if target, err := portforward.URLTarget(dest); err == nil && source.ID != "" {
		if menu.Item("端口转发并打开").Disabled(!a.model.Connected || a.controller == nil || a.preview).Chosen() {
			a.openForwardedConversationLink(parent, source, dest, target)
		}
	} else if ui.SafeToOpen(dest) {
		if menu.Item("在浏览器中打开").Chosen() {
			a.activateConversationLink(parent, source, sourcePath, dest)
		}
	} else if !strings.HasPrefix(dest, "#") {
		if path, _, err := mobileFileLinkTarget(sourcePath, dest); err == nil && source.ID != "" {
			filePath = path
			if menu.Item("预览文件").Chosen() {
				a.activateConversationLink(parent, source, sourcePath, dest)
			}
		}
	}
	web := mobileDownloadableURL(dest)
	if source.ID != "" && (web || filePath != "") {
		if menu.Item("另存为…").Disabled(a.preview || !a.model.Connected || a.controller == nil).Chosen() {
			resource := filePath
			if web {
				resource = dest
			}
			a.exportConversationLink(parent, source, resource, web)
		}
	}
	if menu.Item("复制链接").Chosen() {
		mygo.Clipboard.WriteText(dest)
	}
	menu.EditItems()
}

func (a *mobileApp) activateConversationLink(parent *mygo.Window, source domain.Session, sourcePath, dest string) {
	if target, err := portforward.URLTarget(dest); err == nil && (target.Host == "localhost" || loopbackAddress(target.Host)) {
		a.openForwardedConversationLink(parent, source, dest, target)
		return
	}
	if ui.SafeToOpen(dest) {
		if err := mygo.Shell.OpenExternal(dest); err != nil {
			a.linkError(err)
		}
		return
	}
	target, position, err := mobileFileLinkTarget(sourcePath, dest)
	if err != nil {
		a.linkError(err)
		return
	}
	a.openLinkedFile(parent, source, sourcePath, &mobileLinkedPreview{target: target, position: position})
}

func loopbackAddress(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

func (a *mobileApp) linkError(err error) {
	a.model.Error = err.Error()
	a.refresh()
}

func (a *mobileApp) visibleLinkSource(parent *mygo.Window, source domain.Session) bool {
	if !a.pageCurrent(parent) || source.ID == "" {
		return false
	}
	current := a.sessionByID(source.ID)
	if current.WorkspaceID != source.WorkspaceID || current.ThreadID != source.ThreadID {
		return false
	}
	if parent == a.detail {
		root := a.splitRoot()
		if !root.wide {
			return false
		}
		if review := root.currentSplitReview(); review.state != nil {
			return review.owner == a && review.state.session.ID == source.ID
		}
		if a.parallelOwner != nil {
			return a.model.SessionID == source.ID
		}
		return a.model.SessionID == source.ID || a.parallelDevice == nil && a.parallel == source.ID
	}
	return parent != a.chat || a.model.SessionID == source.ID
}

func (a *mobileApp) openForwardedConversationLink(parent *mygo.Window, source domain.Session, dest string, target portforward.Target) {
	if !a.visibleLinkSource(parent, source) {
		return
	}
	root := a.splitRoot()
	m, controller, manager := a.model, a.controller, root.forwards
	device := m.Snapshot.DeviceID
	if a.preview || manager == nil || controller == nil || !m.Connected || device == "" ||
		source.Context.DeviceID != "" && source.Context.DeviceID != device {
		a.linkError(errors.New("连接会话所属设备后再打开网页"))
		return
	}
	if root.forwardedPageOpen {
		return
	}
	root.forwardedPageOpen = true
	epoch := m.ConnectionEpoch
	generation := m.Snapshot.Generation
	ctx, cancel := context.WithCancel(context.Background())
	unregister := parent.OnClosed(cancel)
	req := portforward.Request{ID: domain.NewID(), DeviceID: device, Name: a.endpoint.Address,
		Target: target, LocalPort: target.Port, AutoPort: true}
	dial := root.forwardDial(device)
	visible := func() bool {
		return ctx.Err() == nil && a.model == m && a.controller == controller && m.ConnectionEpoch == epoch &&
			m.Snapshot.Generation == generation && a.visibleLinkSource(parent, source)
	}
	go func() {
		probe, done := context.WithTimeout(ctx, 20*time.Second)
		mapping, err := manager.Create(probe, req, dial)
		if err == nil && mapping.State != "available" {
			err = manager.Retry(probe, mapping.ID)
		}
		done()
		finish := func(err error) {
			mygo.RunOnMain(func() {
				defer cancel()
				defer unregister()
				root.forwardedPageOpen = false
				if err != nil && visible() {
					a.linkError(err)
				}
			})
		}
		if err != nil {
			finish(err)
			return
		}
		mygo.RunOnMain(func() {
			if !visible() {
				if mapping.ID == req.ID {
					go manager.CloseID(req.ID)
				}
				finish(nil)
				return
			}
			window := parent.NativeHandle()
			go func() {
				finish(showManagedForward(ctx, window, manager, mapping.ID, dest))
			}()
		})
	}()
}

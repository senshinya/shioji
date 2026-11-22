package main

/*
#cgo LDFLAGS: -framework UIKit -framework ImageIO
#include <stdlib.h>
int shiojiIsTablet(void);
int shiojiIsSimulator(void);
unsigned char *shiojiImageThumbnail(const char *path, int *width, int *height);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"slices"
	"strings"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"shioji/internal/mobileclient"
)

func isTablet() bool    { return C.shiojiIsTablet() != 0 }
func isSimulator() bool { return C.shiojiIsSimulator() != 0 }

func readMobileThumbnail(path string) (*ui.Bitmap, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var width, height C.int
	pixels := C.shiojiImageThumbnail(name, &width, &height)
	if pixels == nil {
		return nil, errors.New("无法读取图片")
	}
	defer C.free(unsafe.Pointer(pixels))
	return ui.NewBitmap(&image.RGBA{
		Pix: C.GoBytes(unsafe.Pointer(pixels), width*height*4), Stride: int(width) * 4,
		Rect: image.Rect(0, 0, int(width), int(height)),
	}), nil
}

var uikit = mygo.Native("shiojiUIKit")

func (a *mobileApp) openNativeFilePreview(parent *mygo.Window, path, name, mime string) {
	a.tryNativeFilePreview(parent, path, name, mime, func(shown *bool, err error) {
		if err == nil && shown != nil && !*shown {
			a.linkError(errors.New("此文件不支持系统预览"))
		}
	})
}

func (a *mobileApp) tryNativeFilePreview(parent *mygo.Window, path, name, mime string, done func(*bool, error)) {
	if a.pageCurrent(parent) && path != "" {
		var shown *bool
		a.callUIKitResult("quickLook", mobilePreviewArgs(parent, path, name, mime), &shown, func(err error) {
			done(shown, err)
		})
	}
}

func mobilePreviewArgs(parent *mygo.Window, path, name, mime string) map[string]any {
	return map[string]any{"window": parent.NativeHandle(), "path": path, "name": name, "mime": mime}
}

func (a *mobileApp) callUIKit(method string, args any, done func(error)) {
	a.callUIKitResult(method, args, nil, done)
}

func (a *mobileApp) callUIKitResult(method string, args, result any, done func(error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := uikit.Call(ctx, method, args, result)
		if err != nil || done != nil {
			mygo.RunOnMain(func() {
				if err != nil {
					a.model.Error = err.Error()
					a.refresh()
				}
				if done != nil {
					done(err)
				}
			})
		}
	}()
}

func (a *mobileApp) configureUIKit() {
	args := map[string]any{"home": a.home.NativeHandle(), "devices": a.devices.NativeHandle(),
		"settings": a.settings.NativeHandle(), "minimumWidth": mobileclient.SidebarMinWidth,
		"composeIcon": tabIconImage(composeSymbol), "appearance": nativeAppearance(),
		"shortcuts": slices.Concat(conversationShortcuts, interfaceShortcuts)}
	if isTablet() {
		detail := a.page("", a.detailView)
		args["detail"] = detail.NativeHandle()
		a.detail = detail
		a.detailQueueMenu.listen(a, detail)
	}
	uikit.On("searchActive", func(raw []byte) {
		var active bool
		if json.Unmarshal(raw, &active) == nil {
			a.inlineSearch.cancel()
			if active {
				a.inlineSearch = mobileContentSearch{}
				a.sessionListMenu.publish(a, a.home, contextMenuFrame{}, ui.Rect{})
			}
			a.searching = active
			a.home.Invalidate()
		}
	})
	uikit.On("composerKeyboardDismissed", func(raw []byte) {
		var window uintptr
		if json.Unmarshal(raw, &window) == nil {
			a.composerKeyboardDismissed = window
			a.refresh()
		}
	})
	uikit.On("keyboardMotion", func(raw []byte) {
		if json.Unmarshal(raw, &a.keyboardMotion) == nil {
			for _, w := range []*mygo.Window{a.chat, a.detail} {
				if w != nil {
					w.Invalidate()
				}
			}
		}
	})
	uikit.On("searchQuery", func(raw []byte) {
		var query string
		if a.searching && json.Unmarshal(raw, &query) == nil {
			a.inlineSearch.change(query)
			if strings.TrimSpace(query) != "" {
				a.sessionListMenu.publish(a, a.home, contextMenuFrame{}, ui.Rect{})
			}
			a.home.Invalidate()
		}
	})
	uikit.On("searchSubmit", func(_ []byte) {
		if a.searching && strings.TrimSpace(a.inlineSearch.query) != "" {
			a.inlineSearch.submit(a, a.home)
		}
	})
	uikit.On("searchMove", func(raw []byte) {
		var direction int
		if a.searching && json.Unmarshal(raw, &direction) == nil && a.inlineSearch.move(direction, a.inlineSearch.hits()) {
			a.home.Invalidate()
		}
	})
	uikit.On("prominentCompose", func(_ []byte) {
		a.prominentCompose = true
		a.refresh()
	})
	uikit.On("compose", func(_ []byte) { a.openDraft(a.home) })
	uikit.On("navigationShortcut", a.navigationShortcut)
	uikit.On("linkExportCanceled", func(raw []byte) {
		var id string
		if current := a.linkExport; current != nil && json.Unmarshal(raw, &id) == nil && current.id == id {
			current.cancel()
		}
	})
	uikit.On("wideChanged", func(raw []byte) {
		var wide bool
		if json.Unmarshal(raw, &wide) != nil {
			return
		}
		wasWide := a.wide
		if wasWide && !wide {
			a.suspendRightPaneFind()
		}
		a.wide, a.widthReported = wide, true
		if a.draftPage == nil {
			if wide && a.chat != nil {
				a.callUIKit("popPage", map[string]uintptr{"window": a.chat.NativeHandle()}, nil)
			} else if wasWide && !wide && a.model.SessionID != "" && a.chat == nil {
				a.pushConversation(a.home)
			}
		}
		a.openPreviewRoute()
		a.openQueuedNotification()
		a.refresh()
	})
	uikit.On("formDismissed", func(raw []byte) {
		var id uintptr
		if json.Unmarshal(raw, &id) != nil {
			return
		}
		if w := a.draftPage; w != nil && w.NativeHandle() == id {
			w.Close()
		}
	})
	a.callUIKit("configureHome", args, func(err error) {
		a.uiConfigured = err == nil
		a.openPreviewRoute()
		a.openQueuedNotification()
	})
}

func (a *mobileApp) navigationShortcut(raw []byte) {
	var event struct {
		Action string
		Window uintptr
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if a.handleZoomShortcut(event.Action, event.Window) {
		return
	}
	if draft := a.draftPage; draft != nil {
		if event.Action == "attention" && event.Window == draft.NativeHandle() && a.pageCurrent(draft) {
			a.openNextAttention(draft)
		}
		return
	}
	if archive := a.archivePages[event.Window]; event.Action == "previous" && a.pageCurrent(archive) {
		archive.Pop()
		return
	}
	var source *mygo.Window
	for _, page := range []*mygo.Window{a.home, a.detail, a.chat, a.devices, a.settings} {
		if page != nil && page.NativeHandle() == event.Window && a.pageCurrent(page) {
			source = page
			break
		}
	}
	if source == nil {
		return
	}
	switch event.Action {
	case "search":
		a.toggleSessionSearch(source == a.home || source == a.detail)
	case "conversation-find":
		if source == a.chat || source == a.detail && a.wide {
			a.findShortcutPage = source
			source.Invalidate()
		}
	case "compose":
		if source == a.chat {
			a.openDraft(source)
			return
		}
		a.callUIKit("selectTab", map[string]int{"index": 0}, func(err error) {
			if err == nil {
				a.openDraft(a.home)
			}
		})
	case "previous":
		location := a.previousConversation
		if location.SessionID == "" || a.sessionListOpening != nil {
			return
		}
		a.cancelAttentionShortcut()
		catalog := a.sessionCatalog(location.DeviceID).Snapshot
		archived := false
		for _, session := range catalog.Sessions {
			if session.ID == location.SessionID && session.WorkspaceID == location.WorkspaceID {
				archived = session.Archived
				break
			}
		}
		for _, workspace := range catalog.Workspaces {
			if workspace.ID == location.WorkspaceID {
				archived = archived || workspace.Archived
				break
			}
		}
		a.callUIKit("selectTab", map[string]int{"index": 0}, func(err error) {
			if err == nil && a.draftPage == nil {
				a.openSessionListTarget(sessionListTarget{location: location, archived: archived})
			}
		})
	case "attention":
		a.openNextAttention(source)
	case "settings":
		a.cancelAttentionShortcut()
		a.callUIKit("selectTab", map[string]int{"index": 2}, nil)
	}
}

func (a *mobileApp) toggleSessionSearch(fromSplit bool) {
	a.cancelAttentionShortcut()
	if fromSplit && a.wide && a.detail != nil && !a.searching {
		// Sidebar search leaves the detail surface mounted. Move the Go
		// focus off its editor before UIKit activates the search field.
		a.searchAfterDetailBlur = true
		a.detail.Invalidate()
		return
	}
	a.showSessionSearch()
}

func (a *mobileApp) showSessionSearch() {
	anchor := a.searchAnchor
	a.callUIKit("search", map[string]any{"home": a.home.NativeHandle(), "toggle": true,
		"anchor": [4]float32{anchor.X, anchor.Y, anchor.W, anchor.H}, "reduceMotion": a.model.Preferences.ReduceMotion}, nil)
}

func (a *mobileApp) openPreviewRoute() {
	if !a.preview || !a.uiConfigured || isTablet() && !a.widthReported {
		return
	}
	route := a.previewRoute
	a.previewRoute = ""
	if board, ok := strings.CutPrefix(route, "board-"); ok {
		a.openPreviewBoard(board)
		return
	}
	switch route {
	case "session":
		if len(a.model.Snapshot.Sessions) != 0 {
			a.openSession(a.model.Snapshot.Sessions[0].ID)
		}
	case "compose":
		a.openDraft(a.home)
	}
}

type nativePageOptions struct {
	Window       uintptr `json:"window"`
	Title        string  `json:"title,omitempty"`
	Subtitle     string  `json:"subtitle,omitempty"`
	InlineScope  bool    `json:"inlineScope,omitempty"`
	Commit       bool    `json:"commit,omitempty"`
	ReduceMotion bool    `json:"reduceMotion,omitempty"`
}

func (a *mobileApp) configureNativePage(page *mygo.Window, options nativePageOptions, done func(error)) {
	options.Window = page.NativeHandle()
	options.ReduceMotion = a.model.Preferences.ReduceMotion
	a.callUIKit("configurePage", options, done)
}

func (a *mobileApp) pushNative(parent, page *mygo.Window) {
	a.pushNativePage(parent, page, nativePageOptions{})
}

func (a *mobileApp) pushWithCommit(parent, page *mygo.Window, title string, commit func()) {
	mygo.SetBarItems(page, mygo.BarItem{Title: title, Symbol: "checkmark", OnClick: commit})
	a.pushNativePage(parent, page, nativePageOptions{Commit: true})
}

func (a *mobileApp) pushNativePage(parent, page *mygo.Window, options nativePageOptions) {
	a.configureNativePage(page, options, func(err error) {
		if err == nil && a.pageCurrent(parent) && a.pageCurrent(page) {
			parent.Push(page)
		} else {
			page.Close()
		}
	})
}

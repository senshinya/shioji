package main

import (
	"encoding/json"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type contextMenuCommand struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Symbol      string `json:"symbol"`
	Destructive bool   `json:"destructive,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	run         func()
}

type contextMenuRow struct {
	Key      string               `json:"key"`
	Rect     [4]float32           `json:"rect"`
	Preview  bool                 `json:"preview"`
	Message  *contextMenuMessage  `json:"message,omitempty"`
	Commands []contextMenuCommand `json:"commands,omitempty"`
}

type contextMenuMessage struct {
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

type contextMenuTarget struct {
	location app.SessionLocation
	session  domain.Session
	open     func()
	message  *contextMenuMessage
	commands []contextMenuCommand
}

type contextMenuFrame struct {
	rows    []contextMenuRow
	targets map[string]contextMenuTarget
	zoom    float32
}

func (f *contextMenuFrame) add(row ui.Element, key string, target contextMenuTarget, clips ...ui.Element) {
	if target.session.ID == "" && target.message == nil && len(target.commands) == 0 {
		return
	}
	if f.targets == nil {
		f.targets = make(map[string]contextMenuTarget)
	}
	f.targets[key] = target
	row.DrawOver(func(_ *ui.Painter, _ ui.Rect) {
		// A row retained for its exit transition no longer owns a menu target.
		if !row.Valid() {
			return
		}
		r := row.Bounds()
		for _, clip := range clips {
			r = intersectMenuRect(r, clip.Bounds())
		}
		if r.W <= 0 || r.H <= 0 {
			return
		}
		f.rows = append(f.rows, contextMenuRow{Key: key, Rect: [4]float32{r.X, r.Y, r.W, r.H},
			Preview: target.session.ID != "", Message: target.message, Commands: target.commands})
	})
}

func intersectMenuRect(r, clip ui.Rect) ui.Rect {
	left, top := max(r.X, clip.X), max(r.Y, clip.Y)
	right, bottom := min(r.X+r.W, clip.X+clip.W), min(r.Y+r.H, clip.Y+clip.H)
	return ui.Rect{X: left, Y: top, W: max(0, right-left), H: max(0, bottom-top)}
}

type mobileContextMenu struct {
	sequence int
	payload  string
	targets  map[string]contextMenuTarget
	preview  *archiveConversationPreview
}

func (m *mobileContextMenu) publish(a *mobileApp, w *mygo.Window, f contextMenuFrame, viewport ui.Rect) {
	m.targets = f.targets
	visible := make([]contextMenuRow, 0, len(f.rows))
	for _, row := range f.rows {
		r := intersectMenuRect(ui.Rect{X: row.Rect[0], Y: row.Rect[1], W: row.Rect[2], H: row.Rect[3]}, viewport)
		if r.W > 0 && r.H > 0 {
			rect := mobileWindowRect(r, f.zoom)
			row.Rect = [4]float32{rect.X, rect.Y, rect.W, rect.H}
			visible = append(visible, row)
		}
	}
	data, _ := json.Marshal(visible)
	if string(data) == m.payload {
		return
	}
	m.payload = string(data)
	m.sequence++
	a.callUIKit("rowContextMenus", map[string]any{"window": w.NativeHandle(), "sequence": m.sequence, "rows": json.RawMessage(data)}, nil)
}

func queueContextMenuFrame(c *ui.Context) *contextMenuFrame {
	return ui.Local(c.Root(), "queue-context-menus", func() contextMenuFrame { return contextMenuFrame{} })
}

func (m *mobileContextMenu) drawQueues(c *ui.Context, a *mobileApp, w *mygo.Window) {
	frame := queueContextMenuFrame(c)
	*frame = contextMenuFrame{zoom: c.Zoom()}
	c.Root().DrawOver(func(_ *ui.Painter, viewport ui.Rect) {
		if w != nil {
			m.publish(a, w, *frame, viewport)
		}
	})
}

func (m *mobileContextMenu) listen(a *mobileApp, w *mygo.Window) {
	off := uikit.On("rowContextAction", func(data []byte) {
		var event struct {
			Window               uintptr
			Key, Action, Request string
		}
		if json.Unmarshal(data, &event) != nil || event.Window != w.NativeHandle() {
			return
		}
		if event.Action == "closePreview" {
			if m.preview != nil && m.preview.request == event.Request {
				m.preview.close()
				m.preview = nil
			}
			return
		}
		if !a.pageCurrent(w) {
			return
		}
		target, found := m.targets[event.Key]
		if !found {
			return
		}
		switch event.Action {
		case "preview":
			if target.session.ID != "" && event.Request != "" {
				m.preview.close()
				m.preview = a.startArchiveConversationPreview(w, target.location, target.session, event.Request)
			}
		case "open":
			if target.open != nil {
				target.open()
			}
		default:
			if i := slices.IndexFunc(target.commands, func(command contextMenuCommand) bool { return command.ID == event.Action && !command.Disabled }); i >= 0 {
				target.commands[i].run()
			}
		}
	})
	w.OnClosed(func() {
		off()
		m.preview.close()
		m.preview = nil
	})
}

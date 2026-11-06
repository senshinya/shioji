package ui

import (
	"github.com/egoist/mygo/transfer"
	"strings"
)

// VirtualTextSelection lets a virtual list retain a message's native selection
// while its rows are unmounted. source resolves stable SelectionItem keys to
// current source text; removal or replacement invalidates the selection. Only
// the selected paragraphs and offsets are retained, never the offscreen UI.
func VirtualTextSelection(element Element, source func(key string) (string, bool)) {
	if e := element.node(); e != nil {
		e.virtualTextSource = source
		if e.c.rt.virtualText == nil {
			support := &virtualTextSupport{rt: e.c.rt}
			e.c.rt.virtualText = &virtualTextHooks{clear: func() { support.retainedText = retainedTextSelection{} }, keepMissing: support.keepUnmountedTextSelection, restore: support.restoreRetainedTextSelection, remember: support.retainTextSelection, copy: support.copyRetainedSelection, copyAllowed: support.retainedCopyAllowed, releaseFocusRow: support.releaseFocusRow}
		}
	}
}

// SelectionItem identifies selectable content by its model identity rather
// than its current position in a virtual list.
func SelectionItem(element Element, key string) {
	if e := element.node(); e != nil {
		e.logicalTextKey = key
	}
}

type virtualTextSupport struct {
	rt           *engine
	retainedText retainedTextSelection
}
type retainedTextSelection struct {
	owner       uint64
	key, source string
	selection   textSelection
	parts       []SelectedText
	data        transfer.Data
}

func (support *virtualTextSupport) retainedSelectionValid() bool {
	rt := support.rt
	r := &support.retainedText
	if r.owner == 0 || r.selection.scope != rt.selection.scope || r.selection.anchor != rt.selection.anchor || r.selection.caret != rt.selection.caret {
		return false
	}
	owner := rt.states[r.owner]
	if owner == nil || owner.seen != rt.frame || owner.pass != rt.pass || owner.virtualTextSource == nil {
		return false
	}
	source, ok := owner.virtualTextSource(r.key)
	if !ok || !strings.HasPrefix(source, r.source) {
		return false
	}
	return true
}
func (support *virtualTextSupport) retainTextSelection() {
	rt := support.rt
	scope := rt.states[rt.selection.scope]
	if scope == nil {
		support.retainedText = retainedTextSelection{}
		return
	}
	key := ""
	var owner *state
	for s := scope; s != nil; s = rt.states[s.parent] {
		if key == "" && s.logicalTextKey != "" {
			key = s.logicalTextKey
		}
		if s.virtualTextSource != nil {
			owner = s
			break
		}
		if s.parent == 0 {
			break
		}
	}
	if key == "" || owner == nil {
		support.retainedText = retainedTextSelection{}
		return
	}
	source, ok := owner.virtualTextSource(key)
	if !ok {
		support.retainedText = retainedTextSelection{}
		return
	}
	parts := rt.selectedTexts(rt.selection.scope)
	if len(parts) == 0 {
		support.retainedText = retainedTextSelection{}
		return
	}
	previous := &support.retainedText
	same := previous.owner == owner.id && previous.key == key && previous.selection.anchor == rt.selection.anchor && previous.selection.caret == rt.selection.caret && len(previous.parts) == len(parts)
	if same {
		for i, p := range parts {
			if p != previous.parts[i] {
				same = false
				break
			}
		}
	}
	if same {
		previous.source = source
		previous.selection = rt.selection
		return
	}
	var data transfer.Data
	if scope.copyData != nil {
		data = scope.copyData(parts)
	} else {
		var out strings.Builder
		for i, p := range parts {
			if i > 0 {
				out.WriteByte('\n')
			}
			runes := []rune(p.Text)
			out.WriteString(string(runes[min(p.Start, len(runes)):min(p.End, len(runes))]))
		}
		data = transfer.TextData(out.String())
	}
	support.retainedText = retainedTextSelection{owner: owner.id, key: key, source: source, selection: rt.selection, parts: parts, data: data}
}
func (support *virtualTextSupport) keepUnmountedTextSelection() bool {
	rt := support.rt
	if !support.retainedSelectionValid() {
		support.retainedText = retainedTextSelection{}
		return false
	}
	r := &support.retainedText
	// Keep keyboard Copy attached to this list only while it owned focus.
	for s := rt.states[rt.focused]; s != nil; s = rt.states[s.parent] {
		if s.id == rt.selection.scope {
			rt.focused = r.owner
			break
		}
		if s.parent == 0 {
			break
		}
	}
	rt.selection.dragging = false
	return true
}

func (support *virtualTextSupport) retainedSelectionOwnsFocus() bool {
	rt := support.rt
	for s := rt.states[rt.focused]; s != nil; s = rt.states[s.parent] {
		if s.id == rt.selection.scope {
			return true
		}
		if s.parent == 0 {
			break
		}
	}
	return false
}
func (support *virtualTextSupport) restoreRetainedTextSelection() bool {
	rt := support.rt
	if support.retainedText.owner == 0 {
		return true
	}
	if !support.retainedSelectionValid() {
		support.retainedText = retainedTextSelection{}
		return true
	}
	for _, part := range support.retainedText.parts {
		if s := rt.states[part.ID]; s != nil && s.seen == rt.frame && s.editor != nil && !strings.HasPrefix(s.editor.source, part.Text) {
			support.retainedText = retainedTextSelection{}
			return false
		}
	}
	if rt.focused == support.retainedText.owner {
		rt.focused = rt.selection.caret.id
	}
	return true
}
func (support *virtualTextSupport) retainedCopyAllowed() bool {
	rt := support.rt
	return rt.windowFocused && rt.modal == 0 && rt.focused == support.retainedText.owner && support.retainedSelectionValid()
}
func (support *virtualTextSupport) copyRetainedSelection() bool {
	rt := support.rt
	if !support.retainedCopyAllowed() {
		return false
	}
	rt.host.writeClipboardData(support.retainedText.data)
	return true
}

func (support *virtualTextSupport) releaseFocusRow(element *node) bool {
	return support.retainedText.owner == element.id && support.retainedText.selection.scope == support.rt.selection.scope && len(support.retainedText.parts) > 0 && support.retainedSelectionOwnsFocus()
}

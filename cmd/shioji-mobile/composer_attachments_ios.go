package main

import (
	"context"
	"os"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func (a *mobileApp) composerAttachmentMenu(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, title string, size float32) {
	chip := editor.chipMaker(c)
	zoom := c.Zoom()
	var button ui.Element
	button = iconButton(c, title, plusSymbol, size, func() {
		if editor.attachmentMenuOpen || editor.pending() || !editor.current(a, parent) {
			return
		}
		rect := mobileWindowRect(button.LayoutBounds(), zoom)
		controller := a.controller
		args := map[string]any{"window": parent.NativeHandle(), "rect": []float32{rect.X, rect.Y, rect.W, rect.H}}
		start, end := editor.Doc.Selection()
		if !editor.focused {
			start = editor.Doc.Text().Len()
			end = start
		}
		version := editor.Doc.Version()
		editor.attachmentMenuOpen = true
		go func() {
			var choice struct{ Action, Text, Path string }
			err := uikit.Call(context.Background(), "composerAttachments", args, &choice)
			mygo.RunOnMain(func() {
				editor.attachmentMenuOpen = false
				if a.controller != controller || editor.pending() || !editor.current(a, parent) {
					if choice.Path != "" {
						os.Remove(choice.Path)
					}
					return
				}
				if err != nil {
					editor.attachmentError = err.Error()
					a.refresh()
					return
				}
				switch choice.Action {
				case "reference":
					editor.openReferences(a, parent, chip)
				case "file":
					a.localAttachment(parent, editor.target, editor)
				case "image":
					if !a.stageLocalAttachment(parent, editor.target, editor, func() (string, []byte, error) {
						defer os.Remove(choice.Path)
						data, err := os.ReadFile(choice.Path)
						return "粘贴图片.png", data, err
					}) {
						os.Remove(choice.Path)
					}
				case "text":
					if editor.Doc.Version() != version {
						return
					}
					editor.Doc.Edit(func(edit *richtext.Edit) {
						from := min(start, end)
						edit.ReplaceString(from, max(start, end), choice.Text, richtext.Attributes{})
						caret := from + utf8.RuneCountInString(choice.Text)
						edit.Select(caret, caret)
					})
					if editor.Publish(a.model, editor.target.key) {
						a.save()
						a.refresh()
					}
				}
			})
		}()
	}).KeepFocus()
}

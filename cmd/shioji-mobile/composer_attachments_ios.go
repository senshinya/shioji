package main

import (
	"context"
	"os"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/attachments"
	"shioji/internal/domain"
)

func (a *mobileApp) composerAttachmentMenu(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, title string, size float32) ui.Element {
	chip := editor.chipMaker(c)
	zoom := c.Zoom()
	var button ui.Element
	button = iconButton(c, title, plusSymbol, size, func() {
		if editor.attachmentMenuOpen || editor.pending() || !editor.current(a, parent) {
			return
		}
		rect := mobileWindowRect(button.LayoutBounds(), zoom)
		controller, epoch := a.controller, a.model.ConnectionEpoch
		remaining := domain.MaxMessageAttachments
		for _, file := range a.model.Attachments[editor.target.key] {
			if file.Attachment.Kind != domain.DiffCommentResource {
				remaining--
			}
		}
		args := map[string]any{"window": parent.NativeHandle(), "rect": []float32{rect.X, rect.Y, rect.W, rect.H}, "photoLimit": max(0, remaining)}
		start, end := editor.Doc.Selection()
		if !editor.focused {
			start = editor.Doc.Text().Len()
			end = start
		}
		version := editor.Doc.Version()
		editor.attachmentMenuOpen = true
		go func() {
			var choice struct {
				Action, Text, Path string
				Photos             []struct{ Path, Name string }
			}
			err := uikit.Call(context.Background(), "composerAttachments", args, &choice)
			cleanPhotos := func() {
				for _, photo := range choice.Photos {
					os.Remove(photo.Path)
				}
			}
			mygo.RunOnMain(func() {
				editor.attachmentMenuOpen = false
				if a.controller != controller || a.model.ConnectionEpoch != epoch || editor.pending() || !editor.current(a, parent) {
					if choice.Path != "" {
						os.Remove(choice.Path)
					}
					cleanPhotos()
					return
				}
				if err != nil {
					cleanPhotos()
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
				case "photos":
					if !a.stageLocalAttachments(parent, editor.target, editor, func(staging *attachments.Repository) ([]domain.Attachment, error) {
						defer cleanPhotos()
						items := make([]domain.Attachment, 0, len(choice.Photos))
						for _, photo := range choice.Photos {
							data, err := os.ReadFile(photo.Path)
							if err != nil {
								return nil, err
							}
							item, err := staging.Save(photo.Name, data)
							if err != nil {
								return nil, err
							}
							items = append(items, item)
						}
						return items, nil
					}) {
						cleanPhotos()
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
	return button
}

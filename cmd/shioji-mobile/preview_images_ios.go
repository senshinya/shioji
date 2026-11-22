package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"path/filepath"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

func (a *mobileApp) seedSystemFilePreview(sessionID string) {
	data, err := base64.StdEncoding.DecodeString(os.Getenv("SHIOJI_FIXTURE_PREVIEW_BYTES"))
	if err != nil {
		return
	}
	name := os.Getenv("SHIOJI_FIXTURE_PREVIEW_NAME")
	if len(data) == 0 || name == "" || name != filepath.Base(name) {
		return
	}
	directory, err := os.MkdirTemp("", "shioji-quick-look-fixture-")
	if err != nil {
		return
	}
	a.directory = directory
	local := filepath.Join(directory, name)
	if err := os.WriteFile(local, data, 0600); err != nil {
		return
	}
	a.previewLinkedResource = app.LinkedFile{
		Path:     local,
		Resource: domain.FileResource{Path: name, Name: name, MIME: http.DetectContentType(data), Size: int64(len(data))},
	}
	detail := a.model.Details[sessionID]
	detail.Turns, detail.Queue.Items = nil, nil
	detail.Items = []domain.Item{{ID: "quick-look-file", Kind: "assistant", Final: true, Text: "[打开文件](" + name + ")"}}
	a.model.Details[sessionID] = detail
}

func (a *mobileApp) seedPreviewImages(sessionID string) {
	directory, err := os.MkdirTemp("", "shioji-preview-images-")
	if err != nil {
		a.setupError = err.Error()
		return
	}
	a.directory = directory
	store := attachments.New(filepath.Join(directory, "attachments"))
	sent, err := previewChart(store, "reconnect-before.png", color.NRGBA{R: 47, G: 111, B: 162, A: 255})
	if err != nil {
		a.setupError = err.Error()
		return
	}
	result, err := previewChart(store, "reconnect-after.png", color.NRGBA{R: 24, G: 130, B: 99, A: 255})
	if err != nil {
		a.setupError = err.Error()
		return
	}
	detail := a.model.Details[sessionID]
	detail.Turns = nil
	detail.Items = []domain.Item{
		{ID: "image-prompt", Kind: "user", Status: "sent", Text: "检查这张重连延迟截图。", Attachments: []domain.Attachment{sent}},
		{ID: "image-tool", Kind: "tool", Presentation: "image", Name: "生成重连延迟图", Status: "completed", Final: true,
			Parts: []domain.ContentPart{{Kind: "image", Attachment: &result}}},
		{ID: "image-reply", Kind: "assistant", Text: "连接恢复后，延迟已稳定。", Final: true},
	}
	detail.Queue.Items = nil
	a.model.Details[sessionID] = detail
}

func previewChart(store *attachments.Repository, name string, accent color.NRGBA) (domain.Attachment, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 420, 240))
	fill := func(rect image.Rectangle, value color.Color) {
		draw.Draw(img, rect, image.NewUniform(value), image.Point{}, draw.Src)
	}
	fill(img.Bounds(), color.NRGBA{R: 246, G: 247, B: 249, A: 255})
	fill(image.Rect(26, 24, 134, 32), color.NRGBA{R: 42, G: 51, B: 64, A: 255})
	for y := 64; y <= 200; y += 34 {
		fill(image.Rect(26, y, 394, y+1), color.NRGBA{R: 213, G: 217, B: 225, A: 255})
	}
	for i, height := range []int{108, 76, 119, 56, 43, 37, 42, 36, 38} {
		x := 39 + i*39
		fill(image.Rect(x, 200-height, x+22, 200), accent)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		return domain.Attachment{}, err
	}
	item, err := attachments.Describe(name, data.Bytes())
	if err != nil {
		return domain.Attachment{}, err
	}
	_, err = store.Put(attachments.StagingWorkspace, item, 0, data.Bytes())
	return item, err
}

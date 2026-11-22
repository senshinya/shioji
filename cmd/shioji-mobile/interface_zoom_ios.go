package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	mobileBaselineZoom float32 = 1.12
	mobileMinZoom              = 75
	mobileMaxZoom              = 150
	mobileZoomStep             = 5
)

func zoomPercent(factor float32) int { return int(math.Round(float64(factor * 100))) }

func zoomLabel(percent int) string { return fmt.Sprintf("%d%%", percent) }

func (a *mobileApp) interfaceZoom(c *ui.Context) ui.Element {
	current := zoomPercent(a.model.Preferences.InterfaceZoom)
	return formChoice(c, "界面缩放", zoomLabel(current), func(menu *ui.Menu) {
		for percent := mobileMinZoom; percent <= mobileMaxZoom; percent += mobileZoomStep {
			choice := percent
			menuChoice(menu, zoomLabel(choice), choice == current, func() { a.setInterfaceZoom(choice, nil) })
		}
	})
}

func (a *mobileApp) setInterfaceZoom(percent int, source *mygo.Window) {
	percent = min(mobileMaxZoom, max(mobileMinZoom, percent))
	factor := float32(percent) / 100
	if a.model.Preferences.InterfaceZoom != factor {
		a.model.Preferences.InterfaceZoom = factor
		a.save()
	}
	a.zoomFeedback = source
	a.refresh()
}

func (a *mobileApp) handleZoomShortcut(action string, window uintptr) bool {
	percent := zoomPercent(a.model.Preferences.InterfaceZoom)
	switch action {
	case "zoom-in":
		percent += mobileZoomStep
	case "zoom-out":
		percent -= mobileZoomStep
	case "zoom-reset":
		percent = 100
	default:
		return false
	}
	for page := range a.windows {
		if page.NativeHandle() == window && a.pageCurrent(page) {
			a.setInterfaceZoom(percent, page)
			break
		}
	}
	return true
}

func (a *mobileApp) showZoomFeedback(c *ui.Context, page *mygo.Window) {
	if page != nil && a.zoomFeedback == page {
		a.zoomFeedback = nil
		c.AddToast(ui.Toast{ID: "interface-zoom", Title: "缩放 " + zoomLabel(zoomPercent(a.model.Preferences.InterfaceZoom)), Timeout: 1500 * time.Millisecond})
	}
}

func mobileTapSize(c *ui.Context) float32 { return tapSize / min(1, c.Zoom()) }

// UIKit popovers use the window's points while MyGo lays out in zoomed points.
func mobileWindowRect(r ui.Rect, zoom float32) ui.Rect {
	return ui.Rect{X: r.X * zoom, Y: r.Y * zoom, W: r.W * zoom, H: r.H * zoom}
}

package main

import (
	"bytes"
	"image/color"
	"image/png"
	"math"
	"sync"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// Lucide glyphs match the approved mobile artboard. See third_party/lucide/LICENSE.
func mobileSymbol(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	pencilSymbol        = mobileSymbol(`<path d="M21.17 7.83a2 2 0 0 0-5-5L4 15l-2 7 7-2Z"></path><path d="m15 4 5 5"></path>`)
	swapSymbol          = mobileSymbol(`<path d="M8 3 4 7l4 4M4 7h16m-4 6 4 4-4 4m4-4H4"></path>`)
	plusSymbol          = mobileSymbol(`<path d="M12 5v14M5 12h14"/>`)
	panelSymbol         = mobileSymbol(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	moreSymbol          = mobileSymbol(`<circle cx="12" cy="12" r="1"></circle><circle cx="19" cy="12" r="1"></circle><circle cx="5" cy="12" r="1"></circle>`)
	searchSymbol        = mobileSymbol(`<path d="m21 21-4.34-4.34"></path><circle cx="11" cy="11" r="8"></circle>`)
	readOnlySymbol      = mobileSymbol(`<path d="M2.1 12s3.6-7 9.9-7 9.9 7 9.9 7-3.6 7-9.9 7-9.9-7-9.9-7Z"/><circle cx="12" cy="12" r="3"/>`)
	refreshSymbol       = mobileSymbol(`<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 8a7 7 0 0 1 11.5-2L20 12M4 12l2.4 6a7 7 0 0 0 11.5-2"/>`)
	filterSymbol        = mobileSymbol(`<path d="M2 5h20"></path><path d="M6 12h12"></path><path d="M9 19h6"></path>`)
	folderSymbol        = mobileSymbol(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"></path>`)
	copySymbol          = mobileSymbol(`<rect width="14" height="14" x="8" y="8" rx="2" ry="2"></rect><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"></path>`)
	terminalSymbol      = mobileSymbol(`<path d="M12 19h8"></path><path d="m4 17 6-6-6-6"></path>`)
	compactSymbol       = mobileSymbol(`<path d="m14 10 7-7M20 10h-6V4m-11 17 7-7M4 14h6v6"/>`)
	permissionSymbol    = mobileSymbol(`<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"></path>`)
	verifiedHostSymbol  = mobileSymbol(`<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/>`)
	keySymbol           = mobileSymbol(`<path d="m13.5 10.5-10 10H2v-3l8.5-8.5"/><circle cx="16" cy="7" r="5"/>`)
	keyboardSymbol      = mobileSymbol(`<rect x="2" y="5" width="20" height="14" rx="2"/><path d="M6 9h.01M10 9h.01M14 9h.01M18 9h.01M6 13h.01M18 13h.01M10 15h4"/>`)
	serverSymbol        = mobileSymbol(`<rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/><path d="M6 7h.01M6 17h.01"/>`)
	accountSymbol       = mobileSymbol(`<circle cx="12" cy="7" r="4"/><path d="M4 21v-2a4 4 0 0 1 4-4h8a4 4 0 0 1 4 4v2"/>`)
	networkSymbol       = mobileSymbol(`<rect x="9" y="2" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="16" y="16" width="6" height="6" rx="1"/><path d="M12 8v4M5 16v-4h14v4"/>`)
	processorSymbol     = mobileSymbol(`<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6" rx="1"/><path d="M9 2v2m6-2v2m-6 16v2m6-2v2M2 9h2m-2 6h2m16-6h2m-2 6h2"/>`)
	memorySymbol        = mobileSymbol(`<rect x="2" y="7" width="20" height="10" rx="2"/><path d="M6 11h2m3 0h2m3 0h2M6 17v3m4-3v3m4-3v3m4-3v3"/>`)
	storageSymbol       = mobileSymbol(`<path d="m4 13 2-8h12l2 8"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 16.5h.01m4 0h.01"/>`)
	downloadSymbol      = mobileSymbol(`<path d="M12 3v12m-4-4 4 4 4-4M3 15v4a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-4"/>`)
	parentFolderSymbol  = mobileSymbol(`<path d="M20 18h-7a5 5 0 0 1-5-5V4m-5 5 5-5 5 5"/>`)
	pickerSymbol        = mobileSymbol(`<path d="m7 15 5 5 5-5"></path><path d="m7 9 5-5 5 5"></path>`)
	composeSymbol       = mobileSymbol(`<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z"></path>`)
	rightSymbol         = mobileSymbol(`<path d="m9 18 6-6-6-6"></path>`)
	backSymbol          = mobileSymbol(`<path d="m15 18-6-6 6-6"></path>`)
	sendSymbol          = mobileSymbol(`<path d="m5 12 7-7 7 7"></path><path d="M12 19V5"></path>`)
	stopSymbol          = mobileSymbol(`<rect width="18" height="18" x="3" y="3" rx="2"></rect>`)
	checkSymbol         = mobileSymbol(`<path d="M20 6 9 17l-5-5"></path>`)
	deviceSymbol        = mobileSymbol(`<rect width="20" height="14" x="2" y="3" rx="2"></rect><line x1="8" x2="16" y1="21" y2="21"></line><line x1="12" x2="12" y1="17" y2="21"></line>`)
	conversationsSymbol = mobileSymbol(`<path d="M16 10a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 14.286V4a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"></path><path d="M20 9a2 2 0 0 1 2 2v10.286a.71.71 0 0 1-1.212.502l-2.202-2.202A2 2 0 0 0 17.172 19H10a2 2 0 0 1-2-2v-1"></path>`)
	emptyInboxSymbol    = mobileSymbol(`<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="m5.45 5.11-3.45 6.89v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	gearSymbol          = mobileSymbol(`<path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915"></path><circle cx="12" cy="12" r="3"></circle>`)
	fileCodeSymbol      = mobileSymbol(`<path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z"></path><path d="M14 2v5a1 1 0 0 0 1 1h5"></path><path d="M10 12.5 8 15l2 2.5"></path><path d="m14 12.5 2 2.5-2 2.5"></path>`)
	compareSymbol       = mobileSymbol(`<circle cx="18" cy="18" r="3"></circle><circle cx="6" cy="6" r="3"></circle><path d="M13 6h3a2 2 0 0 1 2 2v7"></path><path d="M11 18H8a2 2 0 0 1-2-2V9"></path>`)
	clockSymbol         = mobileSymbol(`<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`)
	linkSymbol          = mobileSymbol(`<path d="M9 17H7A5 5 0 0 1 7 7h2m6 0h2a5 5 0 0 1 0 10h-2M8 12h8"/>`)
	planSymbol          = mobileSymbol(`<path d="m3 5 2 2 4-4M11 6h10M3 12l2 2 4-4m2 3h10M3 19l2 2 4-4m2 3h10"/>`)
	goalSymbol          = mobileSymbol(`<path d="M20.1 10a8.4 8.4 0 1 1-6.2-6.2M16 12a4 4 0 1 1-4-4m0 4L21 3m-5 0h5v5"/>`)
	downSymbol          = mobileSymbol(`<path d="m6 9 6 6 6-6"></path>`)
	branchSymbol        = mobileSymbol(`<path d="M15 6a9 9 0 0 0-9 9V3"></path><circle cx="18" cy="6" r="3"></circle><circle cx="6" cy="18" r="3"></circle>`)
	forkSymbol          = mobileSymbol(`<circle cx="12" cy="18" r="3"></circle><circle cx="6" cy="6" r="3"></circle><circle cx="18" cy="6" r="3"></circle><path d="M18 9v2a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V9m6 4v2"></path>`)
	infoSymbol          = mobileSymbol(`<circle cx="12" cy="12" r="10"></circle><path d="M12 16v-4"></path><path d="M12 8h.01"></path>`)
	layersSymbol        = mobileSymbol(`<path d="M12 2 2 7l10 5 10-5-10-5Z"/><path d="m2 12 10 5 10-5M2 17l10 5 10-5"/>`)
	filesSymbol         = mobileSymbol(`<path d="M15 2H8a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7Z"/><path d="M14 2v6h6M4 6v14a2 2 0 0 0 2 2h10"/>`)
	closeSymbol         = mobileSymbol(`<path d="m18 6-12 12M6 6l12 12"/>`)
	wifiOffSymbol       = mobileSymbol(`<path d="M12 20h.01"></path><path d="M8.5 16.429a5 5 0 0 1 7 0"></path><path d="M5 12.859a10 10 0 0 1 5.17-2.69"></path><path d="M19 12.859a10 10 0 0 0-2.007-1.523"></path><path d="M2 8.82a15 15 0 0 1 4.177-2.643"></path><path d="M22 8.82a15 15 0 0 0-11.288-3.764"></path><path d="m2 2 20 20"></path>`)
	upSymbol            = mobileSymbol(`<path d="m6 15 6-6 6 6"/>`)
	checkedSymbol       = mobileSymbol(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m8 12 3 3 5-6"/>`)
	appearanceSymbol    = mobileSymbol(`<circle cx="12" cy="12" r="9"/><path d="M12 3v18"/>`)
)

const (
	toolbarIconSize float32 = 19
	inlineIconSize  float32 = 16
	pickerIconSize  float32 = 12
)

func iconButton(c *ui.Context, title string, icon *ui.SVG, size float32, onClick func()) ui.Element {
	p := palette(c)
	return mobileButton(c, title, onClick).Size(mobileTapSize(c), mobileTapSize(c)).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).
		Children(func() {
			ui.Icon(c, icon).Size(size, size).TextColor(p.ink).PassThrough()
		})
}

type glyphReference struct {
	font   ui.Font
	sample string
}

type inlineGlyphMetrics struct {
	center, ascent, descent float32
}

var textCenters sync.Map
var symbolCenters sync.Map

// Preserve the first line's baseline with optical centering against either
// the Latin cap height or the visible body of the Han fallback font.
func inlineGlyphReference(label string, font ui.Font) inlineGlyphMetrics {
	sample := "H"
	for _, r := range label {
		if unicode.Is(unicode.Han, r) {
			sample = "国"
			break
		}
	}
	key := glyphReference{font, sample}
	if metrics, ok := textCenters.Load(key); ok {
		return metrics.(inlineGlyphMetrics)
	}
	ink := font.InkBounds(sample)
	line := ui.ShapeText(sample, font, 0).Lines()[0]
	m := inlineGlyphMetrics{ink.Y + ink.H/2, line.Ascent, line.Descent}
	textCenters.Store(key, m)
	return m
}

func inkCenterOffset(label string, font ui.Font) float32 {
	m := inlineGlyphReference(label, font)
	return m.center + (m.ascent-m.descent)/2
}

// An SVG's painted strokes may be centered away from the viewBox midpoint.
func paintCenteredSymbol(p *ui.Painter, icon *ui.SVG, r ui.Rect, ink ui.Color) {
	center, ok := symbolCenters.Load(icon)
	if !ok {
		const resolution = 128
		img := icon.Image(resolution, resolution, color.White)
		left, top, right, bottom := resolution, resolution, -1, -1
		for y := range resolution {
			for x := range resolution {
				if img.RGBAAt(x, y).A >= 64 {
					left, right = min(left, x), max(right, x)
					top, bottom = min(top, y), max(bottom, y)
				}
			}
		}
		delta := ui.Point{}
		if right >= left {
			delta = ui.Point{X: .5 - float32(left+right+1)/(2*resolution), Y: .5 - float32(top+bottom+1)/(2*resolution)}
		}
		center, _ = symbolCenters.LoadOrStore(icon, delta)
	}
	delta := center.(ui.Point)
	r.X += delta.X * r.W
	r.Y += delta.Y * r.H
	p.Icon(icon, r, ink)
}

func alignedSymbol(c *ui.Context, icon *ui.SVG, labelText string, font ui.Font, size float32, color ui.Color) ui.Element {
	if font.Family == "" {
		font.Family = c.Theme().Font
	}
	return ui.Box(c).Size(size, size).Shrink(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
		r.Y += inkCenterOffset(labelText, font)
		paintCenteredSymbol(p, icon, r, color)
	})
}

const transcriptGlyphSlot = 16

func transcriptGlyph(c *ui.Context, label string, font ui.Font, slot, size float32, draw func(*ui.Painter, ui.Rect)) ui.Element {
	m := inlineGlyphReference(label, font)
	return ui.RichText(c, ui.Span{Text: "\uFFFC", ObjectWidth: slot, ObjectHeight: m.ascent, ObjectDescent: m.descent}).
		Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(mobileStatusLineHeight(font)).
		SingleLine().Role(ui.RoleNone).Shrink(0).PassThrough().DrawObjects(func(p *ui.Painter, _ int, r ui.Rect) {
		draw(p, ui.Rect{X: r.X + (slot-size)/2, Y: r.Y + m.ascent + m.center - size/2, W: size, H: size})
	})
}

func transcriptSymbol(c *ui.Context, icon *ui.SVG, label string, font ui.Font, size float32, ink ui.Color) ui.Element {
	return transcriptGlyph(c, label, font, transcriptGlyphSlot, size, func(p *ui.Painter, r ui.Rect) {
		paintCenteredSymbol(p, icon, r, ink)
	})
}

func mobileBreathingInk(p *ui.Painter, ink ui.Color, pulse bool) ui.Color {
	if !pulse {
		return ink
	}
	const period = 1400 * time.Millisecond
	phase := float64(p.Now().UnixNano()%int64(period)) / float64(period)
	alpha := .4 + .6*float32((1-math.Cos(2*math.Pi*phase))/2)
	p.AnimationFrameRate(30)
	return ink.Alpha(alpha)
}

func transcriptStatusDot(c *ui.Context, label string, font ui.Font, ink ui.Color, live bool) ui.Element {
	const size = 6
	pulse := live && !c.Preferences().ReduceMotion
	return transcriptGlyph(c, label, font, transcriptGlyphSlot, size, func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, mobileBreathingInk(p, ink, pulse), size/2)
	})
}

func iconAction(c *ui.Context, title, accessibleName string, before *ui.SVG, beforeSize float32, after *ui.SVG, afterSize, fontSize float32, weight int, onClick func()) ui.Element {
	p := palette(c)
	font := ui.Font{Size: fontSize * c.Preferences().TextScale, Weight: weight}
	return mobileButton(c, accessibleName, onClick).PaddingX(12).Gap(7).AlignItems(ui.Center).
		Children(func() {
			if before != nil {
				alignedSymbol(c, before, title, font, beforeSize, p.ink)
			}
			oneLine(c, title, fontSize, p.ink).FontWeight(weight).Shrink(1)
			if after != nil {
				alignedSymbol(c, after, title, font, afterSize, p.secondary)
			}
		})
}

// UIKit receives a template at 3× and lays it out in its native toolbar.
func toolbarImage(symbol *ui.SVG) []byte {
	return nativeIconImage(symbol, int(toolbarIconSize))
}

func tabIconImage(symbol *ui.SVG) []byte {
	return nativeIconImage(symbol, 24)
}

func nativeIconImage(symbol *ui.SVG, points int) []byte {
	var pngData bytes.Buffer
	size := points * 3
	if err := png.Encode(&pngData, symbol.Image(size, size, color.Black)); err != nil {
		return nil
	}
	return pngData.Bytes()
}

func toolbarButton(c *ui.Context, title string, symbol *ui.SVG, activate func()) ui.Element {
	return floatingSurface(c, iconButton(c, title, symbol, toolbarIconSize, activate), pillRadius)
}

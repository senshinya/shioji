package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestLongImagePreviewMatchesThumbnailAtBothMotionEndpoints(t *testing.T) {
	for _, size := range []image.Point{{X: 400, Y: 40}, {X: 40, Y: 400}} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d-dark-%v", size.X, size.Y, dark), func(t *testing.T) {
				bitmap := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
				for y := 0; y < size.Y; y++ {
					for x := 0; x < size.X; x++ {
						bitmap.SetRGBA(x, y, color.RGBA{R: uint8(100 + x*100/size.X), G: uint8(40 + y*100/size.Y), B: 30, A: 255})
					}
				}
				a := domain.Attachment{ID: "long-image", Name: "long.png", MIME: "image/png"}
				v := New(layoutModel(), nil, "")
				v.attachmentImages = map[string]*attachmentImage{a.ID: {Image: n.NewBitmap(bitmap)}}
				source := n.Rect{X: 460, Y: 320, W: 48, H: 48}
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, v.Model.Preferences)
					n.Column(c).Fill().Background(theme.Background).Children(func() {
						n.Column(c).Absolute().Left(source.X).Top(source.Y).Children(func() {
							v.attachmentThumbnail(c, theme, a, "w", source.W)
						})
						n.Column(c.Key("other-occurrence")).Absolute().Left(180).Top(100).Children(func() {
							v.attachmentThumbnail(c, theme, a, "w", source.W)
						})
					})
					v.attachmentDialog(c, theme)
				}, 1000, 760)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				rest := ui.Image()
				prefix := fmt.Sprintf("%dx%d-dark-%v", size.X, size.Y, dark)
				saveNativeSkillImage(t, ui, prefix+"-thumbnail.png")
				v.attachmentPreview = &attachmentPreview{Attachment: a, presence: previewOpening, Source: source, Image: v.attachmentImages[a.ID].Image, Origin: &attachmentPreviewOrigin{Radius: 5}}
				ui.Frame()
				saveNativeSkillImage(t, ui, prefix+"-opening.png")
				check := func(stage string) {
					t.Helper()
					actual := ui.Image()
					changed := 0
					for y := 100; y < 600; y++ {
						for x := 200; x < 800; x++ {
							before, after := rest.RGBAAt(x, y), actual.RGBAAt(x, y)
							// Allow one DIP of overlapping antialiasing on the outline.
							// Interior, exterior and outermost corner pixels must match.
							cx := min(x-int(source.X), int(source.X+source.W)-1-x)
							cy := min(y-int(source.Y), int(source.Y+source.H)-1-y)
							if cx >= 0 && cy >= 0 && (cx == 0 || cy == 0 || cx == 1 && cy == 1) && cx+cy > 1 {
								continue
							}
							if math.Abs(float64(before.R)-float64(after.R)) > 4 || math.Abs(float64(before.G)-float64(after.G)) > 4 || math.Abs(float64(before.B)-float64(after.B)) > 4 {
								changed++
							}
						}
					}
					if changed != 0 {
						t.Errorf("%s does not match the cropped thumbnail: %d changed pixels", stage, changed)
					}
				}
				check("opening")
				ui.Advance(60 * time.Millisecond)
				// Once the moving crop has left the source edge, the old image
				// must not stick out behind it as a second colored thumbnail.
				x, y := int(source.X+source.W/2), int(source.Y+1)
				if size.Y > size.X {
					x, y = int(source.X), int(source.Y+source.H/2)
				}
				box, _ := ui.Find("图片预览内容")
				if float32(x)+.5 >= box.X && float32(x)+.5 < box.X+box.W && float32(y)+.5 >= box.Y && float32(y)+.5 < box.Y+box.H {
					t.Fatal("source-edge sample is inside the moving crop", box)
				}
				pixel := ui.Image().RGBAAt(x, y)
				if math.Abs(float64(pixel.R)-float64(pixel.G)) > 3 || math.Abs(float64(pixel.G)-float64(pixel.B)) > 3 {
					t.Error("original thumbnail protrudes behind the moving image", pixel, box)
				}
				other := ui.Image().RGBAAt(204, 124)
				if math.Abs(float64(other.R)-float64(other.G)) <= 3 {
					t.Error("opening one occurrence hid another thumbnail of the same attachment", other)
				}
				ui.Advance(440 * time.Millisecond)
				v.attachmentPreview.close()
				ui.Frame()
				ui.Advance(280 * time.Millisecond)
				saveNativeSkillImage(t, ui, prefix+"-closing.png")
				check("closing")
			})
		}
	}
}

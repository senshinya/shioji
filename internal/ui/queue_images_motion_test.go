package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestQueuedImageDepartureKeepsPixelsAndSettlesWithoutHeightSnap(t *testing.T) {
	for _, scenario := range []string{"empty-queue", "existing-queue", "save-edit", "empty-queue-delayed-event", "existing-queue-delayed-event"} {
		t.Run(scenario, func(t *testing.T) {
			v, ui := nativeQueueFixture()
			ui.SetDark(scenario == "existing-queue")
			v.Model.WorkerMinor = 6
			a := domain.Attachment{ID: domain.NewID(), Name: "photo.png", MIME: "image/png", Size: 100, SHA256: strings.Repeat("a", 64)}
			bitmap := image.NewRGBA(image.Rect(0, 0, 120, 80))
			for y := 0; y < 80; y++ {
				for x := 0; x < 120; x++ {
					bitmap.SetRGBA(x, y, color.RGBA{R: 220, G: 60, B: 40, A: 255})
				}
			}
			v.attachmentImages = map[string]*attachmentImage{a.ID: {Image: n.NewBitmap(bitmap)}}
			q := v.Model.Queue()
			q.Revision++
			if strings.HasPrefix(scenario, "empty-queue") {
				q.Items = nil
			}
			if scenario == "save-edit" {
				q.Items[0].Input.WorkspaceID = "w"
				q.Items[0].Input.Attachments = []domain.Attachment{a}
			} else {
				v.Model.Attachments["session:s"] = []app.DraftAttachment{{Attachment: a, WorkspaceID: "w", Ready: true}}
				v.Model.Drafts["session:s"] = "带图片的下一条消息"
			}
			v.Model.SetQueue(q)
			v.Model.QueueExpanded["s"] = true
			ui.Frame()
			ui.Advance(time.Second)
			if scenario == "save-edit" {
				if err := ui.Click("编辑待发消息 1"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			before, _ := ui.Find("消息编辑器")
			initialPixels := queuedImagePixels(ui.Image())
			saveNativeSkillImage(t, ui, scenario+"-before.png")
			label, method := "加入队列", "turn.enqueue"
			if scenario == "save-edit" {
				label, method = "保存编辑", "queue.edit"
			}
			if err := ui.Click(label); err != nil {
				t.Fatal(err)
			}
			var job app.Job
			for len(v.Controller.Jobs) > 0 {
				candidate := <-v.Controller.Jobs
				if candidate.Method == method {
					job = candidate
				}
			}
			if job.Apply == nil {
				t.Fatal("missing worker operation", method, v.Model.Error)
			}
			q = v.Model.Queue()
			q.Revision++
			var delayedQueue *domain.QueueState
			if scenario == "save-edit" {
				body, _ := json.Marshal(q)
				job.Apply(v.Model, body, nil)
			} else {
				in := job.Input.(domain.Submission)
				q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: in})
				if strings.HasSuffix(scenario, "delayed-event") {
					delayedQueue = &q
				} else {
					v.Model.SetQueue(q)
				}
				body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
				job.Apply(v.Model, body, nil)
			}
			ui.Frame()
			if len(v.Model.Attachments["session:s"]) != 0 {
				t.Fatal("accepted image was not cleared from the actual draft")
			}
			heights := []float32{}
			pixels := []int{}
			for frame := 0; frame <= 26; frame++ {
				if frame == 7 && delayedQueue != nil {
					v.Model.SetQueue(*delayedQueue)
					ui.Frame()
				}
				box, _ := ui.Find("消息编辑器")
				heights = append(heights, box.H)
				pixels = append(pixels, queuedImagePixels(ui.Image()))
				saveNativeSkillImage(t, ui, fmt.Sprintf("%s-frame-%03d.png", scenario, frame))
				ui.Advance(time.Second / 60)
			}
			if math.Abs(float64(heights[0]-before.H)) > 1 {
				t.Error("acceptance jumps the editor height", before.H, heights)
			}
			if pixels[0] < initialPixels-4 || pixels[3] < 50 {
				t.Error("image vanishes before its departure animation", pixels)
			}
			for i := 20; i < len(heights); i++ {
				if math.Abs(float64(heights[i]-heights[i-1])) > 2 {
					t.Error("editor snaps after the height animation should have slowed", heights)
					break
				}
			}
			t.Log("native editor heights", heights, "image pixels", pixels)
		})
	}
}

func queuedImagePixels(img *image.RGBA) int {
	count := 0
	for y := 350; y < 810; y++ {
		for x := 260; x < 1180; x++ {
			p := img.RGBAAt(x, y)
			if int(p.R) > int(p.G)+40 && int(p.G) > int(p.B)+5 {
				count++
			}
		}
	}
	return count
}

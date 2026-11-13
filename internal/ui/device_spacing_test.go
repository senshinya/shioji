package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestAgentHeaderHoverIsOneSurfaceAcrossItsWidth(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := New(deviceUIModel(), nil, "")
		e := agentEditor([]domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}})
		ui := n.NewTester(func(c *n.Context) { v.agentRows(c, c.Theme(), "local", true, e, false, func() {}) }, 800, 220)
		ui.SetDark(dark)
		ui.Move(100, 10)
		ui.Advance(time.Second)
		image := ui.Image()
		left, right := image.RGBAAt(100, 50), image.RGBAAt(750, 50)
		if left != right {
			t.Fatal("layered header hover", dark, left, right)
		}
	}
}
func TestDeviceSettingsUseSameFortyPixelRowsAndLeftEdge(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "settings"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	worker, ok := ui.Find("Shioji worker")
	if !ok {
		t.Fatal("missing worker")
	}
	background, _ := ui.Find("登录后启动服务")
	details, _ := ui.Find("服务详情")
	// The disclosure's hit surface extends 8 DIP beyond the shared reading edge.
	if math.Abs(float64(worker.X-background.X)) > 1 || math.Abs(float64(worker.X-(details.X+8))) > 1 {
		t.Fatal("left edges differ", worker, background, details)
	}
	if math.Abs(float64(background.Y-worker.Y-40)) > 1 || math.Abs(float64(details.Y+10-background.Y-40)) > 1 {
		t.Fatal("setting row rhythm differs", worker, background, details)
	}
}

func TestWorkerStatusStaysWithIdentityInsteadOfFloatingInControlColumn(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "settings"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	name, _ := ui.Find("Shioji worker")
	status, ok := ui.Find("已就绪")
	if !ok {
		t.Fatal("worker state missing")
	}
	gap := status.X - name.X - name.W
	if gap < 7 || gap > 12 {
		t.Fatal("worker status detached from identity", name, status, gap)
	}
	action, _ := ui.Find("检查执行服务更新")
	if action.X <= status.X+status.W {
		t.Fatal("update action overlaps state")
	}
}

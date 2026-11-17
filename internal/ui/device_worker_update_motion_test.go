package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestDevicePopoverUpdateRemovalAnimatesPanelAndFollowingAction(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, u, c := popoverWorkerFixture(t, app.WorkerAvailable)
			u.SetDark(dark)
			u.Frame()
			before, _ := u.Find("当前设备状态")
			detailsBefore, _ := u.Find("设备详情")
			v.Model.DeviceStates["air"].WorkerService.Phase = app.WorkerCurrent
			u.Frame()
			start, _ := u.Find("当前设备状态")
			if math.Abs(float64(start.H-before.H)) > 0.5 {
				t.Fatal("update removal jumped panel height", before, start)
			}
			u.Advance(60 * time.Millisecond)
			middle, _ := u.Find("当前设备状态")
			detailsMiddle, _ := u.Find("设备详情")
			if middle.H >= before.H-0.5 || middle.H <= before.H-27.5 {
				t.Fatal("no intermediate panel height", before, middle)
			}
			if math.Abs(float64((before.H-middle.H)-(detailsBefore.Y-detailsMiddle.Y))) > 0.5 {
				t.Fatal("details does not follow the same height animation", before, middle, detailsBefore, detailsMiddle)
			}
			if middle.X != before.X || middle.Y != before.Y || middle.W != before.W {
				t.Fatal("height animation moved the popover anchor", before, middle)
			}
			u.Click("浮层执行服务更新")
			if len(c.Jobs) > 0 {
				t.Fatal("outgoing update accepted an action")
			}
			u.Advance(180 * time.Millisecond)
			after, _ := u.Find("当前设备状态")
			detailsAfter, _ := u.Find("设备详情")
			if math.Abs(float64(before.H-after.H-28)) > 0.5 || math.Abs(float64(detailsBefore.Y-detailsAfter.Y-28)) > 0.5 {
				t.Fatal("update row retained its space", before, after, detailsBefore, detailsAfter)
			}
			if _, ok := u.Find("浮层执行服务更新"); ok {
				t.Fatal("update remains mounted after collapse")
			}
		})
	}
}

func TestDevicePopoverUpdateRemovalReversesWithoutJump(t *testing.T) {
	v, u, _ := popoverWorkerFixture(t, app.WorkerAvailable)
	before, _ := u.Find("当前设备状态")
	service := v.Model.DeviceStates["air"].WorkerService
	service.Phase = app.WorkerCurrent
	u.Frame()
	u.Advance(60 * time.Millisecond)
	u.Frame() // Sample the same native animation time before retargeting.
	middle, _ := u.Find("当前设备状态")
	service.Phase = app.WorkerAvailable
	u.Frame()
	reversed, _ := u.Find("当前设备状态")
	if math.Abs(float64(middle.H-reversed.H)) > 0.5 {
		t.Fatal("reversing update visibility jumped", middle, reversed)
	}
	u.Advance(300 * time.Millisecond)
	after, _ := u.Find("当前设备状态")
	if math.Abs(float64(before.H-after.H)) > 0.5 {
		t.Fatal("update did not regain its original height", before, after)
	}
}

func TestDevicePopoverUpdateRemovalHonorsReducedMotion(t *testing.T) {
	v, u, _ := popoverWorkerFixture(t, app.WorkerAvailable)
	u.SetPreferences(n.Preferences{ReduceMotion: true})
	u.Frame()
	before, _ := u.Find("当前设备状态")
	v.Model.DeviceStates["air"].WorkerService.Phase = app.WorkerCurrent
	u.Frame()
	after, _ := u.Find("当前设备状态")
	if math.Abs(float64(before.H-after.H-28)) > 0.5 {
		t.Fatal("reduced motion waited for height animation", before, after)
	}
	if _, ok := u.Find("浮层执行服务更新"); ok {
		t.Fatal("reduced motion retained outgoing update")
	}
}

func TestDevicePopoverUpdateHeightNativeCapture(t *testing.T) {
	dir := os.Getenv("SHIOJI_DEVICE_UPDATE_HEIGHT_CAPTURE")
	if dir == "" {
		t.Skip("opt-in native height transition recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
		v, u, _ := popoverWorkerFixture(t, app.WorkerAvailable)
		u.SetDark(dark)
		u.Frame()
		for frame := 0; frame < 90; frame++ {
			switch frame {
			case 30:
				v.Model.DeviceStates["air"].WorkerService.Phase = app.WorkerCurrent
			case 60:
				v.Model.DeviceStates["air"].WorkerService.Phase = app.WorkerAvailable
			}
			u.Frame()
			u.Advance(time.Second / 60)
			saveNativeSkillImage(t, u, fmt.Sprintf("frame-%04d.png", frame))
			if frame == 29 {
				saveNativeSkillImage(t, u, "before.png")
			}
			if frame == 34 {
				saveNativeSkillImage(t, u, "middle.png")
			}
			if frame == 50 {
				if _, ok := u.Find("浮层执行服务更新"); ok {
					t.Fatal("recording did not reach completed collapse")
				}
				saveNativeSkillImage(t, u, "after.png")
			}
		}
	}
}

func TestDevicePopoverUpdateRemovalHandlesLostServiceState(t *testing.T) {
	v, u, _ := popoverWorkerFixture(t, app.WorkerAvailable)
	state := v.Model.DeviceStates["air"]
	state.Connected = false
	state.WorkerService = nil
	u.Frame()
	u.Advance(300 * time.Millisecond)
	if _, ok := u.Find("浮层执行服务更新"); ok {
		t.Fatal("lost service state kept update row")
	}
}

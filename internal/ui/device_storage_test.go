package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"testing"
	"time"
)

type storageUnavailableNetwork struct{ tailnet.Connection }

func (storageUnavailableNetwork) Route(peer, address string, port int) (string, error) {
	return "", fmt.Errorf("network route unavailable: %s %s %d", peer, address, port)
}

func TestDeviceStorageResolvesTailnetBridgeForEveryRequest(t *testing.T) {
	m := deviceUIModel()
	m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "air-worker", SSH: transport.SSH{
		Target: "user@100.64.0.9", TailnetPeer: "air-peer", Worker: "/worker", Port: 2222,
	}}}
	controller := app.NewController("/local-worker")
	controller.AttachTailnet(storageUnavailableNetwork{})
	v := New(m, controller, "")
	s := &deviceStorageState{epoch: m.DeviceStates["air"].ConnectionEpoch}
	v.deviceStorageStates = map[string]*deviceStorageState{"air": s}
	for _, clean := range []bool{false, true} {
		v.readDeviceStorage("air", s, clean)
		select {
		case update := <-controller.Updates:
			update(m)
		case <-time.After(2 * time.Second):
			t.Fatal("storage request did not complete")
		}
		if s.phase != storageFailed || s.error != "network route unavailable: air-peer 100.64.0.9 2222" {
			t.Fatalf("clean=%t: storage bypassed the current Tailnet bridge: %q", clean, s.error)
		}
	}
}

func TestDeviceStorageTrailingTextSharesCapacityEdge(t *testing.T) {
	for _, width := range []int{520, 824} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%t", width, dark), func(t *testing.T) {
				v := New(deviceUIModel(), nil, "")
				s := &deviceStorageState{phase: storageReady, report: &domain.StorageReport{Usage: domain.StorageUsage{LogBytes: 52 << 20}}}
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).FillWidth().PaddingX(32).Children(func() { v.storageGroup(c, c.Theme(), "存储", s, true, "", func(bool) {}) })
				}, width+64, 480)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				capacity, ok := ui.Find("52 MB")
				if !ok {
					t.Fatal("capacity missing")
				}
				for _, phase := range []storagePhase{storageReady, storageCleaning, storageCleaned, storagePartial} {
					s.phase = phase
					s.report.FreedBytes = 52 << 20
					if phase == storagePartial {
						s.report.Failures = []domain.StorageFailure{{Name: "worker.log.1", Reason: "无法删除文件"}}
					}
					ui.Frame()
					ui.Advance(time.Second)
					caption, ok := ui.Find("存储清理状态")
					if !ok {
						t.Fatal("caption missing")
					}
					if math.Abs(float64(caption.X+caption.W-capacity.X-capacity.W)) > 1 {
						t.Fatalf("phase %d: caption right %.2f, capacity right %.2f", phase, caption.X+caption.W, capacity.X+capacity.W)
					}
					if phase == storagePartial {
						retry, _ := ui.Find("重试")
						if math.Abs(float64(retry.X+retry.W-capacity.X-capacity.W)) > 1 {
							t.Fatal("retry text has a different trailing inset", retry, capacity)
						}
					}
				}
			})
		}
	}
}

// A separate success row or a new caption element must move one of these bounds.
func TestStorageSuccessReplacesRunningCaptionWithoutChangingGroupHeight(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	s := &deviceStorageState{phase: storageCleaning, report: &domain.StorageReport{Usage: domain.StorageUsage{UploadBytes: 448 << 20}}}
	ui := n.NewTester(func(c *n.Context) { v.storageGroup(c, c.Theme(), "存储", s, true, "", func(bool) {}) }, 700, 360)
	ui.Advance(time.Second)
	before, ok := ui.Find("存储清理状态")
	if !ok {
		t.Fatal("missing inline running label")
	}
	groupBefore, _ := ui.Find("存储区域")
	s.finish(domain.StorageReport{FreedBytes: 448 << 20}, nil, true)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("存储清理状态")
	groupAfter, _ := ui.Find("存储区域")
	if !ui.HasText("已释放 448 MB") || ui.HasText("清理中…") {
		t.Fatal("result did not replace running label")
	}
	if math.Abs(float64(before.Y-after.Y)) > 1 || math.Abs(float64(groupBefore.H-groupAfter.H)) > 1 {
		t.Fatal("success added or moved a row", before, after, groupBefore, groupAfter)
	}
}

func TestStorageBusyBlocksRepeatedCleanupAndUnknownIsNotZero(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	s := &deviceStorageState{phase: storageReady, report: &domain.StorageReport{Usage: domain.StorageUsage{LogBytes: 17}}}
	calls := 0
	ui := n.NewTester(func(c *n.Context) {
		v.storageGroup(c, c.Theme(), "存储", s, true, "", func(clean bool) {
			if clean {
				calls++
				s.phase = storageCleaning
			}
		})
	}, 700, 360)
	ui.Click("清理设备可回收内容")
	ui.Frame()
	ui.Click("清理设备可回收内容")
	if calls != 1 {
		t.Fatal("duplicate cleanup", calls)
	}
	s.phase = storageUnloaded
	s.report = nil
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("0 B") {
		t.Fatal("unknown storage is reported as zero")
	}
}

func TestStorageRejectsResultFromPreviousConnection(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	s := &deviceStorageState{phase: storageLoading, epoch: "b", request: 2}
	v.deviceStorageStates = map[string]*deviceStorageState{"air": s}
	report := domain.StorageReport{FreedBytes: 18}
	m.DeviceStates["air"].ConnectionEpoch = "reconnected"
	v.applyStorageResult(m, "air", s, "b", 2, report, nil, true)
	if s.phase != storageLoading {
		t.Fatal("old connection result was applied")
	}
	m.DeviceStates["air"].ConnectionEpoch = "b"
	v.applyStorageResult(m, "air", s, "b", 1, report, nil, true)
	if s.phase != storageLoading {
		t.Fatal("older request was applied")
	}
	v.applyStorageResult(m, "air", s, "b", 2, report, nil, true)
	if s.phase != storageCleaned || s.report.FreedBytes != 18 {
		t.Fatal("current result was rejected")
	}
}

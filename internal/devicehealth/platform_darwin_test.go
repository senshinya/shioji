package devicehealth

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNativeKeepAwakeHoldsAndReleasesBothAssertions(t *testing.T) {
	monitor := New(t.TempDir())
	t.Cleanup(func() {
		if err := monitor.Close(); err != nil {
			t.Error(err)
		}
	})
	assertions := func() []string {
		t.Helper()
		output, err := exec.Command("/usr/bin/pmset", "-g", "assertions").CombinedOutput()
		if err != nil {
			t.Fatalf("pmset -g assertions: %v\n%s", err, output)
		}
		var owned []string
		owner := fmt.Sprintf("pid %d(", os.Getpid())
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), owner) && strings.Contains(line, `named: "Shioji keep awake"`) {
				owned = append(owned, line)
			}
		}
		return owned
	}

	state, err := monitor.SetKeepAwake(true, 0)
	if err != nil || !state.KeepAwake.Enabled {
		t.Fatalf("enable keep awake: %v, %v", state.KeepAwake, err)
	}
	owned := assertions()
	for _, kind := range []string{"PreventUserIdleSystemSleep", "PreventSystemSleep"} {
		if !strings.Contains(strings.Join(owned, "\n"), " "+kind+" named:") {
			t.Errorf("missing %s in macOS assertions: %v", kind, owned)
		}
	}
	if len(owned) != 2 {
		t.Errorf("want exactly two macOS assertions, got %v", owned)
	}

	state, err = monitor.SetKeepAwake(false, state.KeepAwake.Revision)
	if err != nil || state.KeepAwake.Enabled {
		t.Fatalf("disable keep awake: %v, %v", state.KeepAwake, err)
	}
	if owned := assertions(); len(owned) != 0 {
		t.Fatalf("keep awake assertions remained after disabling: %v", owned)
	}
}

func TestNativeSamplingUsesActualCPUAndPhysicalMemory(t *testing.T) {
	read, _ := platform()
	r := read()
	deadline := time.Now().Add(2 * time.Second)
	for r.CPU == nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		r = read()
	}
	if r.CPU == nil || *r.CPU < 0 || *r.CPU > 100 {
		t.Fatal("native CPU sampling unavailable", r)
	}
	if r.Memory == nil || r.Memory.TotalBytes == 0 || r.Memory.UsedBytes > r.Memory.TotalBytes {
		t.Fatal("native memory sampling unavailable", r)
	}
	t.Logf("native CPU %.1f%%; memory %d / %d bytes", *r.CPU, r.Memory.UsedBytes, r.Memory.TotalBytes)
}

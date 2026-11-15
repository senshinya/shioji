package devicehealth

import (
	"testing"
	"time"
)

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

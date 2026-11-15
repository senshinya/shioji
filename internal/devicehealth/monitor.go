// Package devicehealth owns device-local load sampling and Shioji's idle-sleep
// assertion. It has no GUI, transport or harness dependency.
package devicehealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"shioji/internal/domain"
)

type reading struct {
	CPU    *float64
	Memory *domain.DeviceMemory
}
type Monitor struct {
	mu        sync.Mutex
	read      func() reading
	acquire   func() (func() error, error)
	release   func() error
	directory string
	state     domain.DeviceHealth
	closed    bool
}
type savedPower struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

func New(directory string) *Monitor {
	read, acquire := platform()
	return newMonitor(directory, read, acquire)
}
func newMonitor(directory string, read func() reading, acquire func() (func() error, error)) *Monitor {
	m := &Monitor{directory: directory, read: read, acquire: acquire}
	m.state.KeepAwake.Available = true
	b, err := os.ReadFile(filepath.Join(directory, "keep-awake.json"))
	if errors.Is(err, os.ErrNotExist) {
		return m
	}
	var saved savedPower
	if err == nil {
		err = json.Unmarshal(b, &saved)
	}
	if err != nil || saved.Revision < 0 {
		m.state.KeepAwake.Available = false
		m.state.KeepAwake.Error = "无法读取保持唤醒设置"
		return m
	}
	m.state.KeepAwake.Revision = saved.Revision
	if saved.Enabled {
		m.release, err = acquire()
		m.state.KeepAwake.Enabled = err == nil
		if err != nil {
			m.state.KeepAwake.Error = err.Error()
		}
	}
	return m
}
func (m *Monitor) Run(ctx context.Context) {
	m.sample(time.Now())
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			m.sample(at)
		}
	}
}
func (m *Monitor) sample(at time.Time) {
	r := m.read()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	cutoff := at.Add(-time.Minute)
	samples := m.state.Samples
	start := 0
	for start < len(samples) && samples[start].At.Before(cutoff) {
		start++
	}
	samples = append(samples[:0], samples[start:]...)
	m.state.Samples = append(samples, domain.DeviceLoad{At: at.UTC(), CPU: r.CPU, Memory: r.Memory})
	if len(m.state.Samples) > 31 {
		m.state.Samples = m.state.Samples[len(m.state.Samples)-31:]
	}
}
func clonePercent(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}
func (m *Monitor) snapshotLocked() domain.DeviceHealth {
	h := m.state
	h.ObservedAt = time.Now().UTC()
	h.Samples = append([]domain.DeviceLoad(nil), h.Samples...)
	for i := range h.Samples {
		h.Samples[i].CPU = clonePercent(h.Samples[i].CPU)
		if h.Samples[i].Memory != nil {
			v := *h.Samples[i].Memory
			h.Samples[i].Memory = &v
		}
	}
	return h
}
func (m *Monitor) Snapshot() domain.DeviceHealth {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}
func (m *Monitor) save(s savedPower) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(m.directory, ".keep-awake-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(b); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(m.directory, "keep-awake.json"))
}
func (m *Monitor) SetKeepAwake(enabled bool, revision int64) (domain.DeviceHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.state.KeepAwake.Available {
		return m.snapshotLocked(), errors.New("保持唤醒不可用")
	}
	old := m.state.KeepAwake
	if revision != old.Revision {
		return m.snapshotLocked(), errors.New("保持唤醒状态已改变，请核对后重试")
	}
	if enabled == old.Enabled && old.Error == "" {
		return m.snapshotLocked(), nil
	}
	next := savedPower{Enabled: enabled, Revision: old.Revision + 1}
	var release func() error
	if enabled {
		var err error
		release, err = m.acquire()
		if err != nil {
			return m.snapshotLocked(), err
		}
	}
	if err := m.save(next); err != nil {
		if release != nil {
			err = errors.Join(err, release())
		}
		return m.snapshotLocked(), fmt.Errorf("无法保存保持唤醒设置：%w", err)
	}
	if !enabled && m.release != nil {
		if err := m.release(); err != nil {
			return m.snapshotLocked(), errors.Join(err, m.save(savedPower{Enabled: old.Enabled, Revision: old.Revision}))
		}
	}
	m.release = release
	m.state.KeepAwake = domain.KeepAwakeState{Available: true, Enabled: enabled, Revision: next.Revision}
	return m.snapshotLocked(), nil
}
func (m *Monitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	if m.release != nil {
		return m.release()
	}
	return nil
}
func cpuPercent(before, after [4]uint32) *float64 {
	var total, idle uint64
	for i := range before {
		delta := uint64(after[i] - before[i])
		total += delta
		if i == 2 {
			idle = delta
		}
	}
	if total == 0 {
		return nil
	}
	v := 100 * float64(total-idle) / float64(total)
	return &v
}

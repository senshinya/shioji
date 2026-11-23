// Package supervisor owns service control independently of the business writer.
package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"time"
)

type Operation struct {
	Legacy                                                   bool
	Input                                                    domain.WorkerReplacement `json:"input"`
	Phase                                                    string                   `json:"phase"`
	Instance, DeviceID, SourceInstance, PreviousBuild, Error string
	SourcePID                                                int
	SourceSchema, TargetSchema                               int `json:",omitempty"`
	Created                                                  time.Time
	ExpiresAt                                                time.Time `json:"expiresAt,omitempty"`
	HostStartDeadline                                        time.Time `json:",omitempty"`
}

func (o Operation) Terminal() bool {
	switch o.Phase {
	case "applied", "rolled-back", "failed", "blocked":
		return true
	}
	return false
}

type journal struct{ directory string }

const interruptedSwitchError = "服务控制在切换期间中断；保留原数据，请通过兼容安装器修复"

func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && len(id) <= 128
}
func atomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return errors.New("invalid supervisor record")
	}
	return json.NewDecoder(f).Decode(value)
}
func (j journal) load(id string) (Operation, error) {
	var o Operation
	if !safeID(id) {
		return o, errors.New("invalid operation ID")
	}
	err := readJSON(filepath.Join(j.directory, "operations", id+".json"), &o)
	if err == nil && o.Input.ID != id {
		err = errors.New("update receipt ID mismatch")
	}
	return o, err
}
func (j journal) current() (Operation, error) {
	var p struct{ ID string }
	err := readJSON(filepath.Join(j.directory, "current.json"), &p)
	if errors.Is(err, os.ErrNotExist) {
		return Operation{}, nil
	}
	if err != nil {
		return Operation{}, err
	}
	return j.load(p.ID)
}
func (j journal) save(o Operation) error {
	if err := j.saveRecord(o); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(j.directory, "current.json"), struct{ ID string }{o.Input.ID})
}
func (j journal) saveRecord(o Operation) error {
	if !safeID(o.Input.ID) {
		return errors.New("invalid operation ID")
	}
	return atomicJSON(filepath.Join(j.directory, "operations", o.Input.ID+".json"), o)
}
func (j journal) accept(in domain.WorkerReplacement, instance, device string) (Operation, error) {
	if !safeID(in.ID) || (in.Policy != "when-idle" && in.Policy != "immediate") {
		return Operation{}, errors.New("invalid update intent")
	}
	if prior, err := j.load(in.ID); err == nil {
		if prior.Input != in {
			return Operation{}, errors.New("operation ID already binds another update")
		}
		return prior, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Operation{}, err
	}
	if current, err := j.current(); err != nil {
		return Operation{}, err
	} else if current.Input.ID != "" && (!current.Terminal() || current.Phase == "blocked") {
		return Operation{}, errors.New("previous update remains active or blocked")
	}
	created := time.Now().UTC()
	o := Operation{Input: in, Phase: "preparing", Instance: instance, DeviceID: device, PreviousBuild: in.ExpectedBuildID, Created: created, ExpiresAt: created.Add(24 * time.Hour)}
	return o, j.save(o)
}
func (j journal) recover() (Operation, error) {
	o, err := j.current()
	if err != nil || o.Input.ID == "" || o.Terminal() {
		return o, err
	}
	switch o.Phase {
	case "preparing", "waiting":
		o.Phase = "failed"
		o.Error = "服务控制在准备或等待时重启；旧更新不会重放"
	default:
		o.Phase = "blocked"
		o.Error = interruptedSwitchError
	}
	return o, j.save(o)
}

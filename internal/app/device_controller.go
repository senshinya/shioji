package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"time"
)

type deviceSource struct {
	connection DeviceConnection
	controller *Controller
	cancel     context.CancelFunc
}
type deviceFleet struct {
	sources      map[string]*deviceSource
	workerBinary string
}
type ConfigureDevice struct {
	Connection DeviceConnection
	Pending    []string
}
type CheckSSHDevice struct {
	Name string
	SSH  transport.SSH
}

func NewDeviceController(directory, localDeviceID string, connections []DeviceConnection, workerBinary string, restored map[string][]string) *Controller {
	root := NewController(directory)
	root.fleet = &deviceFleet{sources: map[string]*deviceSource{}, workerBinary: workerBinary}
	local := NewController(directory)
	local.ExpectedDeviceID = localDeviceID
	local.RestoreOperations = restored["local"]
	root.fleet.sources["local"] = &deviceSource{controller: local}
	for _, connection := range connections {
		if connection.ID == "" || connection.ID == "local" {
			continue
		}
		child := sshController(connection, restored[connection.ID])
		root.fleet.sources[connection.ID] = &deviceSource{connection: connection, controller: child}
	}
	return root
}
func sshController(connection DeviceConnection, pending []string) *Controller {
	c := NewController("")
	c.ExpectedDeviceID = connection.DeviceID
	c.Dial = connection.SSH.Dial
	c.RestoreOperations = pending
	return c
}
func (c *Controller) SelectDevice(key, id string) {
	c.Do(Job{ConnectionID: key, Method: "$select", Input: id})
}
func (c *Controller) postContext(ctx context.Context, update Update) bool {
	select {
	case c.Updates <- update:
	case <-ctx.Done():
		return false
	}
	c.mu.Lock()
	fn := c.invalidate
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
	return true
}
func (c *Controller) startDevice(ctx context.Context, key string, source *deviceSource) {
	childCtx, cancel := context.WithCancel(ctx)
	source.cancel = cancel
	token := domain.NewID()
	c.postContext(ctx, func(m *Model) {
		if m.DeviceStates[key] == nil {
			m.DeviceStates[key] = &DeviceState{}
		}
		m.DeviceStates[key].sourceToken = token
		m.DeviceStates[key].Connected = false
		m.DeviceStates[key].ConnectionEpoch = ""
		m.RefreshDevices()
	})
	go source.controller.Run(childCtx)
	go func() {
		for {
			select {
			case <-childCtx.Done():
				return
			case update := <-source.controller.Updates:
				if !c.postContext(childCtx, func(m *Model) {
					if state := m.DeviceStates[key]; state != nil && state.sourceToken == token {
						m.ApplyDeviceUpdate(key, update)
					}
				}) {
					return
				}
			}
		}
	}()
}
func (c *Controller) runDevices(ctx context.Context) {
	for key, source := range c.fleet.sources {
		c.startDevice(ctx, key, source)
	}
	defer func() {
		for _, source := range c.fleet.sources {
			source.cancel()
		}
	}()
	checking := false
	var checkCancel context.CancelFunc
	checked := make(chan struct {
		job  Job
		body json.RawMessage
		err  error
	}, 1)
	apply := func(job Job, body json.RawMessage, err error) {
		if job.Apply != nil {
			c.postContext(ctx, func(m *Model) {
				if err == nil && job.Method != "$device.check" {
					key := job.ConnectionID
					if key == "" {
						key = "local"
					}
					err = m.validateDeviceReply(key, job, body)
				}
				job.Apply(m, body, err)
			})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-checked:
			checking = false
			checkCancel = nil
			apply(result.job, result.body, result.err)
		case job := <-c.Jobs:
			if job.Method == "session.search" {
				c.searchDevices(ctx, job)
				continue
			}
			if job.Method == "$device.cancel" {
				if checkCancel != nil {
					checkCancel()
				}
				continue
			}
			if job.Method == "$device.check" {
				if checking {
					apply(job, nil, errors.New("设备检查正在进行"))
					continue
				}
				input, ok := job.Input.(CheckSSHDevice)
				if !ok {
					apply(job, nil, errors.New("invalid SSH device request"))
					continue
				}
				checking = true
				callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				checkCancel = cancel
				go func() {
					defer cancel()
					info, err := input.SSH.Bootstrap(callCtx, c.fleet.workerBinary)
					body, _ := json.Marshal(info)
					select {
					case checked <- struct {
						job  Job
						body json.RawMessage
						err  error
					}{job, body, err}:
					case <-ctx.Done():
					}
				}()
				continue
			}
			if job.Method == "$device.configure" {
				input, ok := job.Input.(ConfigureDevice)
				if !ok || input.Connection.ID == "" || input.Connection.ID == "local" || input.Connection.DeviceID == "" {
					apply(job, nil, errors.New("invalid device binding"))
					continue
				}
				key := input.Connection.ID
				if previous := c.fleet.sources[key]; previous != nil {
					previous.cancel()
				}
				source := &deviceSource{connection: input.Connection, controller: sshController(input.Connection, input.Pending)}
				c.fleet.sources[key] = source
				c.startDevice(ctx, key, source)
				continue
			}
			key := job.ConnectionID
			if key == "" {
				key = "local"
			}
			source := c.fleet.sources[key]
			if source == nil {
				apply(job, nil, &protocol.RemoteError{Code: "notSent", Message: "未连接到工作区所属设备"})
				continue
			}
			delegated := job
			if job.Apply != nil {
				delegated.Apply = func(_ *Model, b json.RawMessage, err error) { apply(job, b, err) }
			}
			if !source.controller.Do(delegated) {
				apply(job, nil, fmt.Errorf("设备操作队列繁忙，请重试"))
			}
		}
	}
}

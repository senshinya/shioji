package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"time"
)

type deviceSource struct {
	ctx        context.Context
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
	Trust *transport.SSHHostKey
	Name  string
	SSH   transport.SSH
}

type SignInSSHDevice struct {
	SSH      transport.SSH
	DeviceID string
	Action   string
	Input    protocol.SignInInput
}

type DeviceSetupCheck struct {
	Preparation *ServicePreparation `json:",omitempty"`
	HostKey     *transport.SSHHostKey
	NeedsKey    bool
	Info        transport.WorkerInfo
	Catalog     []domain.HarnessInfo
}
type CompleteSSHDevice struct {
	SSH      transport.SSH
	DeviceID string
	Agents   protocol.DeviceAgents
}

func inspectSSHSetup(ctx context.Context, ssh transport.SSH, info transport.WorkerInfo) (DeviceSetupCheck, error) {
	result := DeviceSetupCheck{Info: info}
	ssh.Worker = info.Worker
	ssh.StateDirectory = info.Directory
	conn, err := ssh.Dial(ctx)
	if err != nil {
		return result, err
	}
	client, err := protocol.Connect(ctx, conn, info.DeviceID)
	if err != nil {
		conn.Close()
		return result, err
	}
	defer client.Close()
	if client.Minor < 16 {
		return result, errors.New("设备的执行服务需要先更新，才能配置 Agent")
	}
	result.Info.Build = client.Worker
	result.Info.Minor = client.Minor
	var ready domain.Readiness
	err = client.Call(ctx, "runtime.catalog", nil, &ready)
	result.Catalog = ready.Harnesses
	return result, err
}
func completeSSHSetup(ctx context.Context, in CompleteSSHDevice) (protocol.AgentSetupResult, error) {
	var result protocol.AgentSetupResult
	if len(in.Agents.Agents) == 0 {
		return result, errors.New("请至少选择一个 Agent")
	}
	conn, err := in.SSH.Dial(ctx)
	if err != nil {
		return result, err
	}
	client, err := protocol.Connect(ctx, conn, in.DeviceID)
	if err != nil {
		conn.Close()
		return result, err
	}
	defer client.Close()
	if client.Minor < 16 {
		return result, errors.New("请先更新设备的执行服务")
	}
	err = client.Call(ctx, "runtime.agents", in.Agents, &result)
	return result, err
}

func NewDeviceController(directory, localDeviceID string, connections []DeviceConnection, workerBinary string, restored map[string][]string) *Controller {
	root := NewController(directory)
	root.fleet = &deviceFleet{sources: map[string]*deviceSource{}, workerBinary: workerBinary}
	local := NewController(directory)
	local.WorkerBinary = workerBinary
	local.WorkerAutoUpdate = true
	local.ExpectedDeviceID = localDeviceID
	local.RestoreOperations = restored["local"]
	root.fleet.sources["local"] = &deviceSource{controller: local}
	for _, connection := range connections {
		if connection.ID == "" || connection.ID == "local" {
			continue
		}
		child := sshController(connection, restored[connection.ID], workerBinary, directory)
		root.fleet.sources[connection.ID] = &deviceSource{connection: connection, controller: child}
	}
	return root
}
func sshController(connection DeviceConnection, pending []string, binary, updateDirectory string) *Controller {
	connection = connection.Normalized()
	c := NewController("")
	c.workerUpdateDirectory = updateDirectory
	c.WorkerBinary = binary
	ssh := connection.ActiveSSH()
	c.WorkerSSH = &ssh
	c.ExpectedDeviceID = connection.DeviceID
	c.paths = newDevicePaths(connection, nil)
	c.Dial = c.paths.dial
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
	source.ctx = childCtx
	if source.controller.paths != nil && source.controller.paths.open == nil {
		source.controller.resolveSSH = c.TailnetSSH
		source.controller.paths.open = func(ctx context.Context, s transport.SSH) (net.Conn, error) {
			routed, err := c.TailnetSSH(s)
			if err != nil {
				return nil, err
			}
			return routed.Dial(ctx)
		}
	}
	token := domain.NewID()
	c.postContext(ctx, func(m *Model) {
		if m.DeviceStates[key] == nil {
			m.DeviceStates[key] = &DeviceState{}
		}
		m.DeviceStates[key].sourceToken = token
		if source.controller.paths != nil {
			m.DeviceStates[key].dialStream = source.controller.paths.dialStream
		}
		m.DeviceStates[key].Connected = false
		m.DeviceStates[key].ConnectionEpoch = ""
		m.RefreshDevices()
	})
	c.deviceWG.Add(1)
	go func() { defer c.deviceWG.Done(); source.controller.Run(childCtx) }()
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
	c.initPortForwards(ctx)
	if c.Tailnet != nil {
		go c.Tailnet.Run(ctx, func(s tailnet.Snapshot) { c.postContext(ctx, func(m *Model) { m.Tailnet = s }) })
	}
	defer c.signInForwards.Close()
	for key, source := range c.fleet.sources {
		c.startDevice(ctx, key, source)
	}
	defer func() {
		for _, source := range c.fleet.sources {
			source.cancel()
		}
		c.deviceWG.Wait()
	}()
	checking := false
	var checkID uint64
	var checkCancel context.CancelFunc
	defer func() {
		if checkCancel != nil {
			checkCancel()
		}
	}()
	checked := make(chan struct {
		id   uint64
		job  Job
		body json.RawMessage
		err  error
	}, 1)
	apply := func(job Job, body json.RawMessage, err error) {
		if job.Apply != nil {
			c.postContext(ctx, func(m *Model) {
				if err == nil && job.Method != "$device.check" && job.Method != "$device.complete" {
					key := job.ConnectionID
					if key == "" {
						key = "local"
					}
					err = m.validateDeviceReply(key, job, body)
					if err == nil && (job.Method == "session.delete" || job.Method == "workspace.delete") {
						// Receipts update the owning catalog just like archive.deleted events.
						m.ApplyDeviceUpdate(key, func(p *Model) { job.Apply(p, body, nil) })
						return
					}
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
			if result.id != checkID {
				continue
			}
			checking = false
			checkCancel = nil
			apply(result.job, result.body, result.err)
		case job := <-c.Jobs:
			if job.Method == "$worker.recovery" {
				c.runWorkerRecoveryJob(ctx, job)
				continue
			}
			if isForwardJob(job.Method) {
				c.runForwardJob(ctx, job)
				continue
			}
			if job.Method == "$tailnet" {
				if a, ok := job.Input.(tailnet.Action); ok && c.Tailnet != nil {
					c.Tailnet.Do(a)
				}
				continue
			}
			if job.Method == "$device.prepare-service" {
				c.runServicePreparationJob(ctx, job)
				continue
			}

			if job.Method == "$worker.migration.inspect" || job.Method == "$worker.migration" {
				in, ok := job.Input.(WorkerMigrationAction)
				if !ok {
					apply(job, nil, errors.New("invalid migration request"))
					continue
				}
				go func() {
					callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					var out any
					var err error
					if job.Prepare != nil {
						if saveErr := job.Prepare(); saveErr != nil {
							err = &protocol.RemoteError{Code: "notSent", Message: "无法保存准备进度，操作未发送：" + saveErr.Error()}
						}
					}
					if err == nil {
						if job.Method == "$worker.migration.inspect" {
							out, err = c.inspectWorkerMigration(callCtx, in.Target)
						} else {
							out, err = c.runWorkerMigration(callCtx, in)
						}
					}
					body, _ := json.Marshal(out)
					if job.Apply != nil {
						c.postContext(ctx, func(m *Model) { job.Apply(m, body, err) })
					}
				}()
				continue
			}
			if job.Method == "$device.signin" {
				input, ok := job.Input.(SignInSSHDevice)
				if !ok {
					apply(job, nil, errors.New("invalid SSH login request"))
					continue
				}
				go func() {
					callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					var result domain.SignInStatus
					var err error
					input.SSH, err = c.TailnetSSH(input.SSH)
					if err == nil {
						result, err = signInSSHSetup(callCtx, input)
					}
					if err == nil {
						result, err = c.signInForwards.prepare(ctx, input.SSH, input.DeviceID, input.Input, result)
					}
					body, _ := json.Marshal(result)
					if job.Apply != nil {
						c.postContext(ctx, func(m *Model) { job.Apply(m, body, err) })
					}
				}()
				continue
			}
			if job.Method == "session.search" {
				c.searchDevices(ctx, job)
				continue
			}
			if job.Method == "$device.cancel" {
				if checkCancel != nil {
					checkCancel()
				}
				checkID++
				checking, checkCancel = false, nil
				continue
			}
			if job.Method == "$device.check" || job.Method == "$device.complete" {
				if checking {
					apply(job, nil, errors.New("设备检查正在进行"))
					continue
				}
				input, checkOK := job.Input.(CheckSSHDevice)
				complete, completeOK := job.Input.(CompleteSSHDevice)
				if job.Method == "$device.check" && !checkOK || job.Method == "$device.complete" && !completeOK {
					apply(job, nil, errors.New("invalid SSH device request"))
					continue
				}
				checking = true
				checkID++
				id := checkID
				callCtx, cancel := context.WithCancel(ctx)
				checkCancel = cancel
				go func() {
					defer cancel()
					var body json.RawMessage
					var err error
					if job.Method == "$device.complete" {
						completeCtx, completeCancel := context.WithTimeout(callCtx, 10*time.Second)
						defer completeCancel()
						var result protocol.AgentSetupResult
						complete.SSH, err = c.TailnetSSH(complete.SSH)
						if err == nil {
							result, err = completeSSHSetup(completeCtx, complete)
						}
						body, _ = json.Marshal(result)
					} else {
						var result DeviceSetupCheck
						result, err = c.prepareSSHDevice(callCtx, input)
						body, _ = json.Marshal(result)
					}
					if err != nil && callCtx.Err() != nil {
						err = callCtx.Err()
					}
					select {
					case checked <- struct {
						id   uint64
						job  Job
						body json.RawMessage
						err  error
					}{id, job, body, err}:
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
				input.Connection = input.Connection.Normalized()
				key := input.Connection.ID
				if previous := c.fleet.sources[key]; previous != nil {
					if previous.connection.DeviceID == input.Connection.DeviceID && previous.controller.paths != nil {
						previous.connection = input.Connection
						previous.controller.paths.configure(input.Connection)
						apply(job, nil, nil)
						continue
					}
					previous.cancel()
					go c.forwards.CloseDevice(previous.connection.DeviceID)
				}
				source := &deviceSource{connection: input.Connection, controller: sshController(input.Connection, input.Pending, c.fleet.workerBinary, c.workerUpdateDirectory)}
				c.fleet.sources[key] = source
				c.startDevice(ctx, key, source)
				apply(job, nil, nil)
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
			fallbackSSH := source.connection.ActiveSSH()
			if job.Apply != nil {
				delegated.Apply = func(_ *Model, b json.RawMessage, err error) {
					ssh := fallbackSSH
					if active := source.controller.currentSSH(); active != nil {
						ssh = *active
					}
					c.deliverSignInReply(source.ctx, job, ssh, source.controller.ExpectedDeviceID, b, err, apply)
				}
			}
			if !source.controller.Do(delegated) {
				apply(job, nil, fmt.Errorf("设备操作队列繁忙，请重试"))
			}
		}
	}
}

// The developer no-start mode also leaves lifecycle management to its caller.
func (c *Controller) SetWorkerAutoUpdate(enabled bool) {
	if c.fleet != nil {
		c.fleet.sources["local"].controller.WorkerAutoUpdate = enabled
	} else {
		c.WorkerAutoUpdate = enabled
	}
}

func signInSSHSetup(ctx context.Context, in SignInSSHDevice) (domain.SignInStatus, error) {
	if in.DeviceID == "" || in.SSH.Worker == "" || in.SSH.StateDirectory == "" {
		return domain.SignInStatus{}, errors.New("请先检查 SSH 设备")
	}
	if in.Action != "start" && in.Action != "status" && in.Action != "cancel" {
		return domain.SignInStatus{}, errors.New("登录操作无效")
	}
	conn, err := in.SSH.Dial(ctx)
	if err != nil {
		return domain.SignInStatus{}, err
	}
	client, err := protocol.Connect(ctx, conn, in.DeviceID)
	if err != nil {
		conn.Close()
		return domain.SignInStatus{}, err
	}
	defer client.Close()
	if client.Minor < 19 {
		return domain.SignInStatus{}, errors.New("更新设备的执行服务后才能在应用内登录")
	}
	if in.Input.Method == domain.SignInAPI && client.Minor < 21 {
		return domain.SignInStatus{}, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能配置 API"}
	}
	if in.Input.Method == domain.SignInBrowser && client.Minor < 20 {
		return domain.SignInStatus{}, &protocol.RemoteError{Code: "notSent", Message: "更新设备执行服务后才能使用远程浏览器登录"}
	}
	var result domain.SignInStatus
	err = client.Call(ctx, "runtime.signin."+in.Action, in.Input, &result)
	if err == nil && (result.ID != in.Input.ID || result.HarnessID != in.Input.HarnessID) {
		err = errors.New("登录回执与请求不一致")
	}
	return result, err
}

func (c *Controller) prepareSSHDevice(ctx context.Context, in CheckSSHDevice) (DeviceSetupCheck, error) {
	connectCtx, stopConnect := context.WithTimeout(ctx, 10*time.Second)
	defer stopConnect()
	var routeErr error
	in.SSH, routeErr = c.TailnetSSH(in.SSH)
	if routeErr != nil {
		return DeviceSetupCheck{}, routeErr
	}
	if in.Trust != nil {
		if in.Trust.Target != in.SSH.Target || in.Trust.Port != in.SSH.Port {
			return DeviceSetupCheck{}, errors.New("连接地址已改变，请重新核对指纹")
		}
		if err := in.Trust.Trust(connectCtx); err != nil {
			return DeviceSetupCheck{}, err
		}
	}
	// OpenSSH verifies the host before account authentication. Existing trusted
	// configurations need no custom known_hosts parsing or extra scan.
	if err := in.SSH.CheckAuthentication(connectCtx); err != nil {
		if connectCtx.Err() != nil {
			return DeviceSetupCheck{}, connectCtx.Err()
		}
		var authentication *transport.SSHAuthenticationRequired
		if errors.As(err, &authentication) {
			return DeviceSetupCheck{NeedsKey: true}, nil
		}
		var verification *transport.SSHHostVerificationRequired
		if !errors.As(err, &verification) {
			return DeviceSetupCheck{}, err
		}
		key, err := in.SSH.InspectHostKey(connectCtx)
		if err != nil {
			return DeviceSetupCheck{}, err
		}
		if key == nil {
			return DeviceSetupCheck{}, errors.New("主机记录已改变，请重新连接")
		}
		return DeviceSetupCheck{HostKey: key}, nil
	}
	stopConnect()
	info, err := in.SSH.Bootstrap(ctx, c.fleet.workerBinary)
	if err != nil {
		return DeviceSetupCheck{}, err
	}
	if info.Legacy != nil || info.Migration != nil {
		return DeviceSetupCheck{Info: info}, nil
	}
	inspectCtx, stopInspect := context.WithTimeout(ctx, 10*time.Second)
	defer stopInspect()
	if info.Minor < 16 && info.Build != nil && info.Build.SafeUpdate {
		candidate, e := transport.InspectWorker(inspectCtx, c.fleet.workerBinary)
		if e != nil {
			return DeviceSetupCheck{}, e
		}
		prep, e := planServicePreparation(info, candidate)
		if e != nil {
			return DeviceSetupCheck{}, e
		}
		if prep != nil {
			prep.SSH = in.SSH
			prep.Name = in.Name
			return DeviceSetupCheck{Info: info, Preparation: prep}, nil
		}
	}
	return inspectSSHSetup(inspectCtx, in.SSH, info)
}

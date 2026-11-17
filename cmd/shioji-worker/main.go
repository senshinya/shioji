package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
	"shioji/internal/worker"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		var drained *serviceDrained
		if errors.As(err, &drained) {
			os.Exit(43)
		}
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, worker.ErrOwned) {
			os.Exit(supervisor.ExitAlreadyOwned)
		}
		if errors.Is(err, worker.ErrLegacyRecoveryActive) {
			os.Exit(supervisor.ExitRetryableStartup)
		}
		os.Exit(1)
	}
}
func run() error {
	if handled, err := managedexec.ChildMain(os.Args[1:]); handled {
		return err
	}
	dir, err := worker.DefaultDirectory()
	if err != nil {
		return err
	}
	flag.StringVar(&dir, "state-dir", dir, "Private worker state directory")
	serviceMigration := flag.String("service-migrate", "", "Start or run the signed fixed-service installer relay")
	serviceControl := flag.String("service-control", "", "Query or cancel a stable fixed-supervisor operation")
	serviceDevice := flag.String("service-device", "", "Expected authenticated service device")
	serviceID := flag.String("service-id", "", "Stable service update operation identifier")
	serviceChild := flag.Bool("service-child", false, "Run only as the fixed supervisor child")
	probe := flag.Bool("probe", false, "Probe isolated local storage without harnesses or credentials")
	describe := flag.Bool("describe", false, "Print worker build metadata without opening state")
	ensure := flag.Bool("ensure", false, "Start or reuse the developer worker and print connection metadata")
	bridge := flag.Bool("bridge", false, "Forward stdio to this user’s existing private worker")
	recovery := flag.String("recovery-helper", "", "Independent execution recovery: inspect, start, status, cancel or run")
	recoveryID := flag.String("recovery-id", "", "Stable recovery operation")
	recoveryDevice := flag.String("recovery-device", "", "Expected authenticated recovery device")
	managed := flag.Bool("managed-service", false, "Wait for and own the per-user worker under launchd")
	binary := flag.String("codex", "codex", "Compatible Codex executable")
	migration := flag.String("legacy-migration", "", "Inspect an explicitly authorized legacy migration")
	migrationID := flag.String("migration-id", "", "Stable legacy migration operation")
	migrationDevice := flag.String("migration-device", "", "Expected migration device")
	migrationOwner := flag.String("migration-owner", "", "Expected opaque legacy owner")
	flag.Parse()
	build := runtimebuild.Current()
	build.Schema = store.SchemaVersion
	build.SafeUpdate = true
	build.Lifecycle = 1
	build.PrivateControl = supervisor.PrivateControlVersion
	build.Maintenance = true
	build.ContextUsage = true
	build.OutputFiles = true
	build.RecoveryHelper = true
	build.ForceRecovery = true
	if *describe {
		return json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-build", Major: protocol.Major, Minor: protocol.Minor, Worker: &build})
	}
	if *probe {
		owner, e := worker.Acquire(dir)
		if e != nil {
			return e
		}
		defer owner.Close()
		db, e := store.Open(filepath.Join(dir, "state.sqlite"))
		if e != nil {
			return e
		}
		e = db.Close()
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(build)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *serviceMigration != "" {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		switch *serviceMigration {
		case "start":
			var input domain.WorkerReplacement
			if e = json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&input); e != nil {
				return e
			}
			op, e := worker.StartLegacyUpgrade(ctx, dir, exe, *serviceDevice, input)
			return serviceReply(op, e)
		case "run":
			lease := os.NewFile(3, "installer control lease")
			if lease == nil {
				return errors.New("missing installer lease")
			}
			defer lease.Close()
			return worker.RunLegacyUpgrade(ctx, dir, exe, *serviceID, *serviceDevice, lease)
		default:
			return errors.New("未知旧服务安装操作")
		}
	}
	if *serviceControl != "" {
		if *serviceControl == "background-status" {
			state, err := supervisor.BackgroundCall(ctx, dir, supervisor.Request{Action: "background-status", ID: *serviceID, DeviceID: *serviceDevice})
			out := struct {
				Background domain.BackgroundService `json:"background"`
				Error      string                   `json:"error,omitempty"`
			}{Background: state}
			if err != nil {
				out.Error = err.Error()
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		var op supervisor.Operation
		var e error
		switch *serviceControl {
		case "status":
			op, e = supervisor.Query(ctx, dir, *serviceID)
		case "repair":
			var input domain.WorkerReplacement
			if e = json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&input); e == nil {
				if !input.Explicit {
					return serviceReply(op, &protocol.RemoteError{Code: "notSent", Message: "冷修复需要明确更新操作"})
				}
				exe, executableErr := os.Executable()
				e = executableErr
				if e == nil {
					e = transport.EnsureRepairControl(ctx, dir, exe, *binary, *serviceDevice)
				}
				if e == nil {
					op, e = supervisor.Call(ctx, dir, supervisor.Request{Action: "repair", DeviceID: *serviceDevice, Input: input})
				}
			}
		case "cancel":
			op, e = supervisor.Query(ctx, dir, *serviceID)
			if e == nil {
				if *serviceDevice == "" || op.DeviceID != *serviceDevice {
					e = &protocol.RemoteError{Code: "notSent", Message: "服务控制设备身份未确认"}
				} else {
					prior := op
					op, e = supervisor.Call(ctx, dir, supervisor.Request{Action: "cancel", ID: *serviceID, DeviceID: *serviceDevice})
					if e != nil && prior.Legacy {
						op, e = supervisor.CancelLegacy(ctx, dir, *serviceID, *serviceDevice)
					}
				}
			}
		default:
			return errors.New("未知的服务控制操作")
		}
		response := struct {
			Operation   supervisor.Operation `json:"operation"`
			Error, Code string
		}{Operation: op}
		if e != nil {
			response.Error = e.Error()
			response.Code = "receiptUnknown"
			var remote *protocol.RemoteError
			if errors.As(e, &remote) {
				response.Code = remote.Code
			}
		}
		return json.NewEncoder(os.Stdout).Encode(response)
	}
	if *recovery != "" && *recovery != "run" {
		in := domain.WorkerRecoveryInput{ID: *recoveryID, DeviceID: *recoveryDevice}
		var out any
		switch *recovery {
		case "inspect":
			out, err = worker.InspectDeviceRecovery(ctx, dir, *recoveryDevice)
		case "start":
			if e := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&in); e != nil {
				return e
			}
			if in.DeviceID != *recoveryDevice || in.ID != *recoveryID {
				return errors.New("恢复参数与目标设备不一致")
			}
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			out, err = worker.StartDeviceRecovery(ctx, dir, exe, in)
		case "status":
			out, err = worker.DeviceRecoveryStatus(dir, *recoveryID)
		case "cancel":
			out, err = worker.CancelDeviceRecovery(dir, *recoveryID, *recoveryDevice)
		default:
			return errors.New("未知的执行恢复操作")
		}
		if err != nil {
			code := "notSent"
			if *recovery == "status" || *recovery == "cancel" {
				code = "recoveryError"
				if errors.Is(err, os.ErrNotExist) {
					code = "notFound"
				}
			}
			// A start failure may follow journal acceptance; clients must query its ID.
			if *recovery == "start" {
				if _, e := worker.DeviceRecoveryStatus(dir, *recoveryID); e == nil {
					code = "receiptUnknown"
				}
			}
			return json.NewEncoder(os.Stdout).Encode(struct {
				DeviceID  string `json:"deviceId"`
				Error     string `json:"error"`
				ErrorCode string `json:"errorCode"`
			}{*recoveryDevice, err.Error(), code})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if *migration == "inspect" {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		info, e := transport.InspectWorkerInfo(ctx, dir, exe, *migrationDevice)
		if e != nil {
			return e
		}
		if info.Migration != nil {
			progress, e := worker.LegacyMigrationStatus(dir, info.Migration.ID)
			if e != nil {
				return e
			}
			info.Migration = &progress
		}
		return json.NewEncoder(os.Stdout).Encode(info)
	}
	if *migration != "" && *migration != "run" {
		var result domain.WorkerMigrationStatus
		switch *migration {
		case "start":
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			result, err = worker.StartLegacyMigration(ctx, dir, exe, domain.WorkerMigrationInput{ID: *migrationID, DeviceID: *migrationDevice, OwnerRef: *migrationOwner})
		case "status":
			result, err = worker.LegacyMigrationStatus(dir, *migrationID)
		case "resume":
			result, err = worker.ResumeLegacyMigration(ctx, dir, domain.WorkerMigrationInput{ID: *migrationID, DeviceID: *migrationDevice, OwnerRef: *migrationOwner})
		default:
			return errors.New("未知的迁移操作")
		}
		if err != nil {
			code := "migrationError"
			if errors.Is(err, os.ErrNotExist) {
				code = "notFound"
			}
			return json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-migration-error", ID: *migrationID, DeviceID: *migrationDevice, ErrorCode: code, Error: err.Error()})
		}
		if result.DeviceID != *migrationDevice {
			return errors.New("迁移不属于指定设备")
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if *ensure {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		info, e := transport.EnsureLocalWorker(ctx, dir, exe, "")
		if e != nil {
			var legacy *protocol.LegacyWorkerError
			if errors.As(e, &legacy) {
				inspection, inspectErr := transport.InspectLegacyWorker(ctx, dir, legacy.DeviceID)
				if inspectErr != nil {
					return inspectErr
				}
				info.DeviceID, info.Legacy = inspection.Info.DeviceID, &inspection.Info
				return json.NewEncoder(os.Stdout).Encode(info)
			}
			return e
		}
		if info.Migration != nil {
			progress, e := worker.LegacyMigrationStatus(dir, info.Migration.ID)
			if e != nil {
				return e
			}
			info.Migration = &progress
		}
		return json.NewEncoder(os.Stdout).Encode(info)
	}
	if *bridge {
		return worker.Bridge(ctx, dir, os.Stdin, os.Stdout)
	}
	if !*serviceChild && *migration == "" && *recovery == "" {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		return transport.RunWorkerSupervisor(ctx, dir, exe, *binary, *managed)
	}
	if *serviceChild {
		parent := os.Getppid()
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if os.Getppid() != parent || parent <= 1 {
						stop()
						return
					}
				}
			}
		}()
	}
	var s *worker.Server
	startupCtx, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()
	initialized := make(chan struct{})
	var startupErr error
	if *serviceChild {
		lifecycle, e := supervisor.OpenChildLifecycle()
		if e != nil {
			return e
		}
		if lifecycle != nil {
			defer lifecycle.Close()
			go func() {
				accepted, _ := supervisor.ServeLifecycle(lifecycle, func(in supervisor.Control) (domain.WorkerUpdateResult, error) {
					if in.Instance != os.Getenv("SHIOJI_WORKER_INSTANCE") {
						return domain.WorkerUpdateResult{}, errors.New("service instance authentication failed")
					}
					if in.Action != "" && in.Action != "drain" && in.Action != "resume" {
						return domain.WorkerUpdateResult{}, errors.New("unknown lifecycle action")
					}
					if !in.AdmitBefore.IsZero() && !time.Now().Before(in.AdmitBefore) {
						return domain.WorkerUpdateResult{}, errors.New("drain admission attempt expired")
					}
					if !in.ExpiresAt.IsZero() && !time.Now().Before(in.ExpiresAt) {
						return domain.WorkerUpdateResult{}, errors.New("prepared update expired")
					}
					if in.Replacement.Policy == "immediate" && !in.Replacement.Explicit {
						return domain.WorkerUpdateResult{}, errors.New("immediate drain requires explicit authority")
					}
					// Cancel only read-only startup discovery, not the server lifetime.
					// Admission remains the owning worker's responsibility once bound.
					if in.Action != "resume" {
						cancelStartup()
					}
					select {
					case <-initialized:
						if s == nil {
							return domain.WorkerUpdateResult{}, startupErr
						}
						if in.Action == "resume" {
							err := s.ResumeService(in)
							return domain.WorkerUpdateResult{Ready: err == nil}, err
						}
						return s.DrainService(in)
					case <-ctx.Done():
						return domain.WorkerUpdateResult{}, ctx.Err()
					}
				})
				if accepted {
					stop()
				}
			}()
		}
	}
	replacement := make(chan string, 1)
	verifyReplacement := func(callCtx context.Context, in domain.WorkerReplacement) error {
		candidate, err := transport.VerifyStagedWorker(callCtx, dir, in.BuildID)
		if err != nil {
			return err
		}
		if candidate.Schema < store.SchemaVersion {
			return errors.New("候选执行服务无法读取当前数据库")
		}
		return nil
	}
	config := worker.Config{VerifyReplacement: verifyReplacement, CheckStartupBuild: func() error {
		selected, err := transport.ActiveWorkerBuild(dir)
		if err != nil {
			return err
		}
		if selected == "" || selected == build.BuildID {
			return nil
		}
		verify, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if _, err = transport.VerifyStagedWorker(verify, dir, selected); err != nil {
			return err
		}
		path, err := transport.StagedWorkerPath(dir, selected)
		if err != nil {
			return err
		}
		return &selectedWorker{path: path}
	}, BackgroundManaged: *managed, DisableBackground: func(callCtx context.Context, id string) (domain.BackgroundService, error) {
		device, _, err := store.ReadWorkerIdentity(callCtx, dir)
		if err != nil {
			return domain.BackgroundService{NotStarted: true}, err
		}
		return supervisor.BackgroundCall(callCtx, dir, supervisor.Request{Action: "background-stop", ID: id, DeviceID: device, Instance: os.Getenv("SHIOJI_WORKER_INSTANCE")})
	}, Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Executable: *binary}}, ReplaceWorker: func(in domain.WorkerReplacement) error {
		verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		candidate, err := transport.VerifyStagedWorker(verifyCtx, dir, in.BuildID)
		if err != nil {
			return err
		}
		if candidate.Schema < store.SchemaVersion {
			return errors.New("候选执行服务无法读取当前数据库")
		}
		path, err := transport.StagedWorkerPath(dir, in.BuildID)
		if err != nil {
			return err
		}
		if err = transport.SaveActiveWorker(dir, in.BuildID); err != nil {
			return err
		}
		select {
		case replacement <- path:
			return nil
		default:
			return errors.New("已有更新正在进行")
		}
	}}
	if *serviceChild {
		config.CheckStartupBuild = nil
		config.ServiceInstance = os.Getenv("SHIOJI_WORKER_INSTANCE")
		config.ServiceOperation = os.Getenv("SHIOJI_SUPERVISOR_OPERATION")
		config.StartPaused = os.Getenv("SHIOJI_WORKER_PAUSED") == "true"
		if config.ServiceInstance == "" {
			return errors.New("missing fixed supervisor instance")
		}
		config.RequestUpdate = func(in domain.WorkerReplacement) (domain.WorkerUpdateResult, error) {
			if in.ID == "" {
				in.ID = domain.NewID()
			}
			if in.Policy == "" {
				in.Policy = "when-idle"
			}
			call, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			op, e := supervisor.Call(call, dir, supervisor.Request{Action: "start", Input: in})
			return domain.WorkerUpdateResult{Ready: e == nil, OperationID: op.Input.ID, Phase: op.Phase, Reason: op.Error}, e
		}
	}
	workerArgs := os.Args[1:]
	if *migration == "run" {
		// Future replacements must enter the ordinary service path rather than
		// replay this one-time migration operation.
		workerArgs = []string{"--state-dir", dir, "--codex", *binary}
		if *managed {
			workerArgs = append(workerArgs, "--managed-service")
		}
	}
	for {
		if *recovery == "run" {
			workerArgs = []string{"--state-dir", dir, "--codex", *binary}
			s, err = worker.RunDeviceRecovery(ctx, config, *recoveryID)
			if err == nil && s == nil {
				return nil
			}
		} else if *migration == "run" {
			s, err = worker.RunLegacyMigration(ctx, config, *migrationID)
			if err == nil && s == nil {
				return nil
			}
		} else {
			s, err = worker.New(startupCtx, config)
		}
		if !*managed || !errors.Is(err, worker.ErrOwned) && !errors.Is(err, worker.ErrLegacyRecoveryActive) {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	startupErr = err
	close(initialized)
	var selected *selectedWorker
	if errors.As(err, &selected) {
		if ctx.Err() != nil {
			return nil
		}
		return syscall.Exec(selected.path, append([]string{selected.path}, workerArgs...), os.Environ())
	}
	if err != nil {
		return err
	}
	defer s.Close()
	go func() { <-ctx.Done(); s.Close() }()
	err = s.Serve()
	closeErr := s.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if *serviceChild {
		if receipt, ok := s.DrainedReceipt(); ok {
			if err = supervisor.WriteDrained(dir, receipt); err != nil {
				return err
			}
			return &serviceDrained{}
		}
		return nil
	}
	select {
	case path := <-replacement:
		if ctx.Err() != nil {
			return nil
		}
		return syscall.Exec(path, append([]string{path}, workerArgs...), os.Environ())
	default:
		return nil
	}
}

type selectedWorker struct{ path string }

func (e *selectedWorker) Error() string { return "已接纳的执行服务版本已改变" }

type serviceDrained struct{}

func (*serviceDrained) Error() string { return "worker drained" }

func serviceReply(op supervisor.Operation, err error) error {
	reply := struct {
		Operation   supervisor.Operation `json:"operation"`
		Error, Code string
	}{Operation: op}
	if err != nil {
		reply.Error = err.Error()
		reply.Code = "receiptUnknown"
		var remote *protocol.RemoteError
		if errors.As(err, &remote) {
			reply.Code = remote.Code
		}
	}
	return json.NewEncoder(os.Stdout).Encode(reply)
}

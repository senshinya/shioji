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
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"shioji/internal/transport"
	"shioji/internal/worker"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
	build.Maintenance = true
	build.ContextUsage = true
	build.OutputFiles = true
	build.RecoveryHelper = true
	build.ForceRecovery = true
	if *describe {
		return json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-build", Major: protocol.Major, Minor: protocol.Minor, Worker: &build})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
			return json.NewEncoder(os.Stdout).Encode(struct {
				DeviceID string `json:"deviceId"`
				Error    string `json:"error"`
			}{*recoveryDevice, err.Error()})
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
	}, BackgroundManaged: *managed, DisableBackground: func(callCtx context.Context) (domain.BackgroundService, error) {
		exe, _ := os.Executable()
		return transport.DisableRuntimeService(callCtx, dir, exe)
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
	var s *worker.Server
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
			s, err = worker.New(ctx, config)
		}
		if !*managed || !errors.Is(err, worker.ErrOwned) {
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

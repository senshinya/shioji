package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	binary := flag.String("codex", "codex", "Compatible Codex executable")
	flag.Parse()
	build := runtimebuild.Current()
	build.Schema = store.SchemaVersion
	build.SafeUpdate = true
	build.ContextUsage = true
	build.OutputFiles = true
	if *describe {
		return json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-build", Major: protocol.Major, Minor: protocol.Minor, Worker: &build})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *ensure {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		info, e := transport.EnsureLocalWorker(ctx, dir, exe, "")
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(info)
	}
	if *bridge {
		return worker.Bridge(ctx, dir, os.Stdin, os.Stdout)
	}
	replacement := make(chan string, 1)
	s, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Executable: *binary}}, ReplaceWorker: func(in domain.WorkerReplacement) error {
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
		select {
		case replacement <- path:
			return nil
		default:
			return errors.New("已有更新正在进行")
		}
	}})
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
		return syscall.Exec(path, append([]string{path}, os.Args[1:]...), os.Environ())
	default:
		return nil
	}
}

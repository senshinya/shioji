package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/managedexec"
	"shioji/internal/transport"
	"shioji/internal/worker"
	"syscall"
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
	ensure := flag.Bool("ensure", false, "Start or reuse the developer worker and print connection metadata")
	bridge := flag.Bool("bridge", false, "Forward stdio to this user’s existing private worker")
	binary := flag.String("codex", "codex", "Compatible Codex executable")
	flag.Parse()
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
	s, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Executable: *binary}}})
	if err != nil {
		return err
	}
	defer s.Close()
	go func() { <-ctx.Done(); s.Close() }()
	return s.Serve()
}

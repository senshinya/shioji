package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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
	dir, err := worker.DefaultDirectory()
	if err != nil {
		return err
	}
	flag.StringVar(&dir, "state-dir", dir, "Private worker state directory")
	binary := flag.String("codex", "codex", "Compatible Codex executable")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := worker.New(ctx, worker.Config{Directory: dir, CodexExecutable: *binary})
	if err != nil {
		return err
	}
	defer s.Close()
	go func() { <-ctx.Done(); s.Close() }()
	return s.Serve()
}

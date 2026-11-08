// The Runtime.app executable is a native service host, never a GUI frontend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/background"
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
	action := flag.String("action", "status", "status, register or unregister")
	serve := flag.Bool("serve", false, "Run the user-owned execution service")
	stateDir := flag.String("state-dir", "", "Private worker directory (defaults to the current user)")
	plist := flag.String("plist", background.PlistName, "Bundled launch agent plist name")
	flag.Parse()
	if filepath.Base(*plist) != *plist {
		return fmt.Errorf("无效的后台服务标识")
	}
	if !*serve {
		state, err := background.Manage(*action, *plist)
		if err != nil && state.Message == "" {
			state.Message = err.Error()
		}
		json.NewEncoder(os.Stdout).Encode(state)
		return err
	}
	directory, err := worker.DefaultDirectory()
	if *stateDir != "" {
		directory = *stateDir
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("worker directory must be absolute")
		}
	}
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(executable), "shioji-worker")
	build, err := transport.ActiveWorkerBuild(directory)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if build != "" {
		if _, err = transport.VerifyStagedWorker(ctx, directory, build); err != nil {
			return err
		}
		path, err = transport.StagedWorkerPath(directory, build)
	} else {
		_, err = transport.InspectWorker(ctx, path)
	}
	if err != nil {
		return err
	}
	return syscall.Exec(path, []string{path, "--state-dir", directory, "--managed-service", "--codex", transport.ExistingCodex()}, append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"))
}

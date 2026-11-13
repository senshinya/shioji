// The Runtime.app executable is a native service host, never a GUI frontend.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"shioji/internal/background"
	"shioji/internal/tailnet"
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
	networkServe := flag.Bool("network-serve", false, "Run the independent local network service")
	networkDescribe := flag.Bool("network-describe", false, "Describe the network service without opening state")
	networkPreflight := flag.Bool("network-preflight", false, "Read-only check of an existing network identity")
	networkAuthorize := flag.Bool("network-authorize", false, "Allow the native Keychain access prompt during explicit preflight")
	networkDirectory := flag.String("network-dir", "", "Private network identity directory")
	networkPrepare := flag.Bool("network-prepare", false, "Complete a network host handoff independently of the GUI")
	networkCandidate := flag.String("network-candidate", "", "Verified candidate network host")
	networkWorker := flag.String("network-worker", "", "Bundled worker locating the signed runtime")
	networkOperation := flag.String("network-operation", "", "Network preparation operation identifier")
	flag.Parse()
	if *networkDescribe {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Version int
			Role    string
		}{1, "network"})
	}
	if *networkServe || *networkPreflight || *networkPrepare {
		dir := *networkDirectory
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			dir = filepath.Join(home, "Library", "Application Support", "Shioji", "client", "tailnet")
		}
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("网络身份目录必须是绝对路径")
		}
		if *networkPrepare {
			if len(*networkOperation) != 32 {
				return fmt.Errorf("网络准备操作标识无效")
			}
			if _, err := hex.DecodeString(*networkOperation); err != nil {
				return fmt.Errorf("网络准备操作标识无效")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			err := transport.PrepareNetworkHost(ctx, dir, *networkWorker, *networkCandidate)
			if written := transport.CompleteNetworkPreparation(dir, *networkOperation, err); written != nil {
				return written
			}
			return err
		}
		if err := transport.CheckLegacyNetworkOwners(dir); err != nil {
			return err
		}
		if !*networkAuthorize || *networkServe {
			if err := tailnet.DisableKeychainInteraction(); err != nil {
				return err
			}
		}
		if *networkPreflight {
			if err := tailnet.CheckIdentity(dir, func() ([]byte, error) { return tailnet.IdentityKey(dir, false) }); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(struct {
				Version int
				Ready   bool
			}{1, true})
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return tailnet.Serve(ctx, tailnet.New(dir, func() ([]byte, error) { return tailnet.IdentityKey(dir, true) }))
	}
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

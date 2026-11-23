package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/systemexec"
	"shioji/internal/testutil"
	"slices"
	"strings"
	"testing"
	"time"
)

func npmLauncherFixture(t *testing.T) (string, string) {
	t.Helper()
	binary, e := exec.LookPath("codex")
	if e != nil {
		t.Skip("installed Codex unavailable")
	}
	binary, e = filepath.Abs(binary)
	if e != nil {
		t.Fatal(e)
	}
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("Node unavailable")
	}
	root := t.TempDir()
	script := filepath.Join(root, "codex.js")
	entry := filepath.Join(root, "codex")
	source := fmt.Sprintf("#!/usr/bin/env node\nconst {spawnSync}=require('node:child_process');const r=spawnSync(%q,process.argv.slice(2),{stdio:'inherit',env:process.env});process.exit(r.status??1);\n", binary)
	if e = os.WriteFile(script, []byte(source), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(script, entry); e != nil {
		t.Fatal(e)
	}
	return entry, filepath.Dir(node)
}

func isolatedShell(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", dir)
}

func TestInstalledLauncherKeepsItsInvokedSymlinkName(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "volta-shim")
	alias := filepath.Join(root, "codex")
	if e := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, alias); e != nil {
		t.Fatal(e)
	}
	image, e := installedExecutable(alias)
	if e != nil || image.path != alias {
		t.Fatal("resolved file identity replaced the required launcher name", image, e)
	}
}

func TestNpmLauncherUsesExplicitEnvironmentDuringDetectionAndStartup(t *testing.T) {
	entry, nodeDir := npmLauncherFixture(t)
	isolatedShell(t, "")
	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime, e := Start(ctx, Options{Executable: entry, Home: testutil.NewProvider(t).Home, Environment: []domain.EnvironmentVariable{{Name: "PATH", Value: nodeDir + ":/usr/bin:/bin"}}})
	if e != nil {
		t.Fatal("explicit PATH was ignored while probing the npm launcher", e)
	}
	defer runtime.Close()
	if !strings.HasSuffix(runtime.cmd.Path, "/codex") {
		t.Fatal("startup replaced the npm entry", runtime.cmd.Path)
	}
}

func TestNpmLauncherRecoversNodeFromTheOwningUsersLoginShell(t *testing.T) {
	entry, nodeDir := npmLauncherFixture(t)
	isolatedShell(t, "printf 'rc startup noise\\n'\nexport PATH='"+strings.ReplaceAll(nodeDir, "'", "'\\''")+":/usr/bin:/bin'\nexport SHIOJI_PROBE_TEST_VALUE='from-shell'\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime, e := Start(ctx, Options{Executable: entry, Home: testutil.NewProvider(t).Home})
	if e != nil {
		t.Fatal("login-shell Node was not available to the npm launcher", e)
	}
	defer runtime.Close()
	if !strings.Contains(environmentValue(runtime.cmd.Env, "PATH"), nodeDir) {
		t.Fatal("startup discarded the discovered shell PATH")
	}
}

func TestMissingNpmNodeIsReportedAsADependencyFailure(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "codex")
	if e := os.WriteFile(entry, []byte("#!/usr/bin/env node\nconsole.log('unused');\n"), 0700); e != nil {
		t.Fatal(e)
	}
	isolatedShell(t, "export PATH=/usr/bin:/bin\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, e := compatibleExecutable(ctx, entry)
	if e == nil || !strings.Contains(e.Error(), "Node") {
		t.Fatal("missing Node was hidden as generic protocol incompatibility", e)
	}
}

func TestLoginEnvironmentKeepsManagedContextAndExplicitSettings(t *testing.T) {
	raw := "__SHIOJI_LOGIN_ENV__" + "PATH=/node:/usr/bin\x00VALUE=line one\nline two\x00SHIOJI_OWNER=must-not-import\x00CODEX_HOME=/other\x00CODEX_THREAD_ID=other-thread\x00CODEX_APP_TOOLS_PIPE_PATH=/other.sock\x00" + "__SHIOJI_LOGIN_ENV__"
	shell := systemexec.ParseLoginEnvironment(raw)
	for _, key := range []string{"SHIOJI_OWNER", "CODEX_THREAD_ID", "CODEX_APP_TOOLS_PIPE_PATH"} {
		if environmentValue(slices.DeleteFunc(shell, codexManagedEnvironment), key) != "" {
			t.Fatal("shell configuration replaced a worker-owned identity", key)
		}
	}
	base := []string{"PATH=/worker:/usr/bin", "VALUE=worker-value", "CODEX_HOME=/bound"}
	merged := runtimeEnvironment(systemexec.MergeEnvironment(base, shell), "/chosen", []domain.EnvironmentVariable{{Name: "VALUE", Value: "explicit"}})
	if environmentValue(merged, "PATH") != "/node:/usr/bin:/worker" || environmentValue(merged, "VALUE") != "explicit" || environmentValue(merged, "CODEX_HOME") != "/chosen" {
		t.Fatal("environment precedence changed")
	}
	if environmentValue(shell, "VALUE") != "line one\nline two" {
		t.Fatal("NUL-delimited capture lost a multiline value")
	}
}

func TestLauncherProbePreservesTheNameRequiredByVersionManagers(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	root := t.TempDir()
	target := filepath.Join(root, "volta-shim")
	entry := filepath.Join(root, "codex")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$0\" in */codex) ;; *) exit 91;; esac\nexec %q \"$@\"\n", binary)
	if err = os.WriteFile(target, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, entry); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resolved, _, err := compatibleExecutable(ctx, entry)
	if err != nil || resolved != entry {
		t.Fatal("probing bypassed the invoked launcher name", resolved, err)
	}
}

func TestExecutableDiscoveryUsesTheSuppliedEnvironment(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "custom-cli")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := lookPathEnvironment("custom-cli", []string{"PATH=" + root})
	if err != nil || resolved != entry {
		t.Fatal("command lookup ignored the child PATH", resolved, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lookPathEnvironment("custom-cli", []string{"PATH=" + relative}); !errors.Is(err, exec.ErrDot) {
		t.Fatal("implicit relative PATH executed an untrusted current-directory entry", err)
	}
}

func TestNpmInstallationIdentityIncludesItsNodeInterpreter(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "codex")
	node := filepath.Join(root, "node")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0700); err != nil {
		t.Fatal(err)
	}
	old, next := filepath.Join(root, "old-node"), filepath.Join(root, "new-node")
	for _, p := range []string{old, next} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(old, node); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + root}
	first, err := installedExecutable(entry, env)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(node)
	if err = os.Symlink(next, node); err != nil {
		t.Fatal(err)
	}
	second, err := installedExecutable(entry, env)
	if err != nil || first.version == second.version {
		t.Fatal("changing the npm launcher's interpreter retained a stale installation identity", err)
	}
}

func TestNpmAdapterSharesItsEnvironmentWithOwnedExecutorAndTitles(t *testing.T) {
	entry, nodeDir := npmLauncherFixture(t)
	isolatedShell(t, "")
	t.Setenv("PATH", "/usr/bin:/bin")
	provider := testutil.NewProvider(t)
	configPath := filepath.Join(provider.Home, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), "requires_openai_auth=false", "requires_openai_auth=false\nenv_key=\"FIXTURE_API_TOKEN\"", 1))
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	configured, err := (Adapter{Executable: entry, Home: provider.Home}).WithRuntimeOptions(domain.RuntimeOptions{Environment: []domain.EnvironmentVariable{{Name: "PATH", Value: nodeDir + ":/usr/bin:/bin"}, {Name: "FIXTURE_API_TOKEN", Value: "local-fixture-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := managedexec.New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("npm-owned")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	defer scope.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	started, err := configured.Start(ctx, harness.StartOptions{Scope: scope})
	if err != nil {
		t.Fatal("managed npm runtime did not start", err)
	}
	defer started.Close(context.Background())
	runtime := started.(*adapterRuntime)
	want := nodeDir + ":/usr/bin:/bin"
	if environmentValue(runtime.cmd.Env, "PATH") != "/usr/bin:/bin" {
		t.Fatal("managed bootstrap inherited target PATH before its execution grant")
	}
	if runtime.executor == nil || runtime.executor.baseEnv["PATH"] != want {
		t.Fatal("executor did not use the same effective PATH")
	}
	titleEnv, err := runtime.titleAdapter.environment(ctx)
	if err != nil || environmentValue(titleEnv, "PATH") != want {
		t.Fatal("title environment differed from runtime", err)
	}
	titleScope, err := manager.Begin("npm-title")
	if err != nil {
		t.Fatal(err)
	}
	if err = titleScope.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	defer titleScope.Stop(context.Background())
	title, err := runtime.GenerateTitle(ctx, harness.TitleInput{Prompt: "验证 npm 安装的执行环境", Scope: titleScope})
	if err != nil || title != "夹具会话标题" {
		t.Fatal("npm title process did not use the configured Node environment", title, err)
	}
}

func TestLoginShellCodexDirectoryFillsOnlyAnUnsetConfiguration(t *testing.T) {
	shell := systemexec.ParseLoginEnvironment("__SHIOJI_LOGIN_ENV__" + "CODEX_HOME=/shell-config\x00" + "__SHIOJI_LOGIN_ENV__")
	cases := []struct {
		base       []string
		home, want string
	}{{nil, "", "/shell-config"}, {[]string{"CODEX_HOME=/worker-config"}, "", "/worker-config"}, {nil, "/agent-config", "/agent-config"}}
	for _, tc := range cases {
		env := runtimeEnvironment(systemexec.MergeEnvironment(tc.base, shell), tc.home, nil)
		if environmentValue(env, "CODEX_HOME") != tc.want {
			t.Fatal("login-shell native config was lost or overwrote an explicit directory")
		}
	}
}

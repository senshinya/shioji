package antigravity

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

var googleAuthorizationLink = regexp.MustCompile(`https://accounts\.google\.com/o/oauth2/[^[:space:]\x00-\x1f"'<>]+`)

type terminalSignIn struct {
	adapter   Adapter
	cmd       *exec.Cmd
	input     io.WriteCloser
	done      chan struct{}
	waitErr   error
	challenge string
	mu        sync.Mutex
	sent      bool
}

func (a Adapter) BeginSignIn(ctx context.Context, method domain.SignInMethod) (harness.SignIn, domain.SignInChallenge, error) {
	var none domain.SignInChallenge
	if method != domain.SignInBrowser {
		return nil, none, harness.Unsupported("sign-in-method")
	}
	env, _ := systemexec.Environment(ctx, nil)
	if err := ctx.Err(); err != nil {
		return nil, none, err
	}
	path, err := a.executable(env)
	if err != nil {
		return nil, none, err
	}
	dir, err := os.MkdirTemp("", "shioji-agy-signin-")
	if err != nil {
		return nil, none, err
	}
	args := []string{"acp", "--login", "--disable-telemetry"}
	if profile := systemexec.EnvironmentValue(env, "GEMINI_HOME"); filepath.IsAbs(profile) {
		args = append(args, "--gemini-dir", profile)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(env, "AGY_CLI_DISABLE_AUTO_UPDATE=1", "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0",
		"BROWSER=/usr/bin/true", "TERM=dumb")
	output, writer, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, none, err
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	input, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	_ = writer.Close()
	if err != nil {
		_ = output.Close()
		if input != nil {
			_ = input.Close()
		}
		_ = os.RemoveAll(dir)
		return nil, none, errors.New("无法启动设备上的 Antigravity 登录")
	}
	login := &terminalSignIn{adapter: a, cmd: cmd, input: input, done: make(chan struct{})}
	link := make(chan string, 1)
	go func() {
		defer output.Close()
		scan := bufio.NewScanner(output)
		scan.Buffer(make([]byte, 4096), 32<<10)
		for scan.Scan() {
			if found := googleAuthorizationLink.FindString(scan.Text()); len(found) != 0 && len(found) <= 16<<10 {
				select {
				case link <- found:
				default:
				}
			}
		}
	}()
	go func() {
		login.waitErr = cmd.Wait()
		_ = output.Close()
		_ = os.RemoveAll(dir)
		close(login.done)
	}()
	select {
	case login.challenge = <-link:
		return login, domain.SignInChallenge{URL: login.challenge, ManualCode: true}, nil
	case <-login.done:
		if login.waitErr == nil {
			return login, none, nil
		}
		return nil, none, errors.New("Antigravity 登录已结束")
	case <-ctx.Done():
		_ = login.Cancel(context.Background())
		return nil, none, ctx.Err()
	}
}

func (l *terminalSignIn) SubmitCode(ctx context.Context, input string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sent || l.challenge == "" {
		return errors.New("当前登录已提交授权信息")
	}
	code := strings.TrimSpace(input)
	if code == "" || len(code) > 4096 || strings.ContainsAny(input, "\r\n\x00") || !matchesAuthorizationCode(l.challenge, code) {
		return errors.New("请输入本次 Google 登录的授权信息")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-l.done:
		return errors.New("当前登录已结束")
	default:
	}
	if pipe, ok := l.input.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = pipe.SetWriteDeadline(time.Now().Add(3 * time.Second))
	}
	l.sent = true
	_, err := io.WriteString(l.input, code+"\n")
	_ = l.input.Close()
	if err != nil {
		return &harness.DeliveryError{Uncertain: true, Message: "授权信息提交待核对"}
	}
	return nil
}

func matchesAuthorizationCode(challenge, code string) bool {
	if !strings.Contains(code, "://") {
		return !strings.ContainsAny(code, " \t")
	}
	origin, e1 := url.Parse(challenge)
	callback, e2 := url.Parse(code)
	if e1 != nil || e2 != nil || callback.User != nil || callback.Fragment != "" {
		return false
	}
	redirect, err := url.Parse(origin.Query().Get("redirect_uri"))
	return err == nil && redirect.Scheme != "" && callback.Scheme == redirect.Scheme && callback.Host == redirect.Host &&
		callback.Path == redirect.Path && callback.Query().Get("code") != "" && callback.Query().Get("state") == origin.Query().Get("state")
}

func (l *terminalSignIn) Wait(ctx context.Context) (domain.Readiness, error) {
	select {
	case <-ctx.Done():
		_ = l.Cancel(context.Background())
		return domain.Readiness{}, ctx.Err()
	case <-l.done:
	}
	if l.waitErr != nil {
		return domain.Readiness{}, errors.New("Antigravity 未确认授权")
	}
	account := &domain.RuntimeAccount{Mode: "account", Label: "Antigravity", State: "authenticated"}
	probe, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	r, err := l.adapter.Start(probe, harness.StartOptions{CatalogOnly: true, RefreshCatalog: true})
	if err != nil {
		return domain.Readiness{}, &harness.SetupError{Account: account, Message: "已登录，模型目录暂未确认，请重新检测"}
	}
	defer r.Close(context.Background())
	ready := r.Readiness()
	if ready.Account != nil {
		account.Subscription = ready.Account.Subscription
	}
	ready.Account = account
	return ready, nil
}

func (l *terminalSignIn) Cancel(ctx context.Context) error {
	select {
	case <-l.done:
		return nil
	default:
	}
	_ = l.input.Close()
	_ = syscall.Kill(-l.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return nil
	}
}

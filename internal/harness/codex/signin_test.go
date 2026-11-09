package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"syscall"
	"testing"
	"time"
)

func TestSignInCapabilitiesFollowLoginVariantsAndRequiredFields(t *testing.T) {
	data, err := os.ReadFile("../../../design/protocol/codex-0.166.0/types.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var client map[string]any
	json.Unmarshal(document.Definitions["ClientRequest"], &client)
	var definitions map[string]any
	json.Unmarshal(data, &struct {
		Definitions *map[string]any `json:"definitions"`
	}{&definitions})
	client["definitions"] = definitions
	encoded, _ := json.Marshal(client)
	support, err := protocolFromSchema(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !support.browserLogin || !support.deviceLogin {
		t.Fatal("installed login variants are unavailable")
	}
	params := definitions["LoginAccountParams"].(map[string]any)
	for _, v := range params["oneOf"].([]any) {
		p := v.(map[string]any)
		if p["properties"].(map[string]any)["type"].(map[string]any)["enum"].([]any)[0] == "chatgptDeviceCode" {
			p["required"] = []any{"type", "newRequired"}
		}
	}
	encoded, _ = json.Marshal(client)
	support, err = protocolFromSchema(encoded)
	if err != nil || !support.browserLogin || support.deviceLogin {
		t.Fatal("incompatible device login must not disable browser login", support, err)
	}
}

// Replace only the external app-server process; use real pipes, frame routing,
// completion correlation, account verification and process cleanup.
func loginPipe(t *testing.T, mode string) *Runtime {
	t.Helper()
	cmd := exec.Command(os.Args[0], "--fixture-signin", mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{cmd: cmd, stdin: input, pending: map[string]chan wire.Message{}, done: make(chan struct{}), normalizer: NewNormalizer(), protocol: &protocolSupport{browserLogin: true, deviceLogin: true, methods: map[string]bool{"account/login/cancel": true, "account/read": true}}, loginEvents: make(chan json.RawMessage, 8)}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go r.read(output)
	t.Cleanup(func() { r.Close() })
	return r
}
func signInFixture(mode string) {
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var m wire.Message
		json.Unmarshal(scan.Bytes(), &m)
		result := json.RawMessage(`{}`)
		switch m.Method {
		case "account/login/start":
			if mode == "missing-callback" {
				result = json.RawMessage(`{"type":"chatgpt","loginId":"login-1","authUrl":"https://auth.openai.com/login"}`)
			} else if mode == "unsafe-callback" {
				result = json.RawMessage(`{"type":"chatgpt","loginId":"login-1","authUrl":"https://auth.openai.com/login?redirect_uri=http%3A%2F%2F192.168.7.119%3A1455%2Fauth%2Fcallback"}`)
			} else if mode == "browser" {
				result = json.RawMessage(`{"type":"chatgpt","loginId":"login-1","authUrl":"https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&state=fixture"}`)
			} else if mode == "unsafe" {
				result = json.RawMessage(`{"type":"chatgpt","loginId":"bad","authUrl":"file:///etc/passwd"}`)
			} else {
				var p struct{ Type string }
				json.Unmarshal(m.Params, &p)
				if p.Type != "chatgptDeviceCode" {
					os.Exit(3)
				}
				if mode == "early" {
					fmt.Println(`{"method":"account/login/completed","params":{"loginId":"other","success":false}}`)
					fmt.Println(`{"method":"account/login/completed","params":{"loginId":"login-1","success":true}}`)
				}
				result = json.RawMessage(`{"type":"chatgptDeviceCode","loginId":"login-1","verificationUrl":"https://auth.openai.com/codex/device","userCode":"ABCD-EFGH"}`)
			}
		case "model/list":
			result = json.RawMessage(`{"data":[{"model":"model","displayName":"Model"}]}`)
		case "configRequirements/read":
			result = json.RawMessage(`{"requirements":null}`)
		case "account/read":
			result = json.RawMessage(`{"requiresOpenaiAuth":true,"account":{"type":"chatgpt","email":"dev@example.test","planType":"plus"}}`)
		case "account/login/cancel":
			var p struct{ LoginID string }
			json.Unmarshal(m.Params, &p)
			if p.LoginID != "login-1" {
				os.Exit(4)
			}
			result = json.RawMessage(`{"status":"canceled"}`)
		default:
			os.Exit(5)
		}
		out, _ := json.Marshal(wire.Message{ID: m.ID, Result: result})
		fmt.Println(string(out))
	}
}
func TestSignInRetainsCompletionBeforeStartResponseAndChecksIdentity(t *testing.T) {
	r := loginPipe(t, "early")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	login, challenge, err := r.beginSignIn(ctx, domain.SignInDeviceCode)
	if err != nil || challenge.Code != "ABCD-EFGH" || challenge.URL != "https://auth.openai.com/codex/device" {
		t.Fatal(challenge, err)
	}
	if _, err = login.Wait(ctx); err != nil {
		t.Fatal("early or unrelated notification lost", err)
	}
}
func TestSignInRejectsUnsafeAuthorizationURL(t *testing.T) {
	r := loginPipe(t, "unsafe")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := r.beginSignIn(ctx, domain.SignInBrowser); err == nil {
		t.Fatal("unsafe URL accepted")
	}
}

func TestSignInCancelTargetsOnlyItsAuthorization(t *testing.T) {
	r := loginPipe(t, "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	login, _, err := r.beginSignIn(ctx, domain.SignInDeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if err = login.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.done:
	default:
		t.Fatal("cancel returned before authorization process stopped")
	}
}

// Opt-in integration: actual installed app-server, isolated auth storage, start
// and cancel only. Never authorizes an account or touches the user's credentials.
func TestLiveSignInStartCancel(t *testing.T) {
	if os.Getenv("SHIOJI_LIVE_AUTH_PROBE") != "1" {
		t.Skip("explicit isolated authorization probe only")
	}
	for _, method := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode} {
		t.Run(string(method), func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			a := Adapter{Executable: "codex", Home: home}
			login, challenge, err := a.BeginSignIn(ctx, method)
			if err != nil {
				t.Fatal(err)
			}
			if method == domain.SignInBrowser && (challenge.Callback == nil || !challenge.Callback.Valid()) {
				t.Fatal("actual browser callback metadata missing")
			}
			if challenge.URL == "" || method == domain.SignInDeviceCode && challenge.Code == "" {
				t.Fatal("missing authorization challenge")
			}
			if err = login.Cancel(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("start/cancel wrote account credentials", err)
			}
			t.Log("actual app-server authorization challenge and cancellation verified; no account authorized")
		})
	}
}

func TestBrowserSignInExposesOnlyItsLoopbackCallback(t *testing.T) {
	r := loginPipe(t, "browser")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	login, challenge, err := r.beginSignIn(ctx, domain.SignInBrowser)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Cancel(context.Background())
	if challenge.Callback == nil || challenge.Callback.Host != "localhost" || challenge.Callback.Port != 1455 {
		t.Fatal("remote browser cannot forward the callback", challenge.Callback)
	}
	if challenge.Code != "" {
		t.Fatal("browser flow acquired a device code")
	}
}

func TestBrowserSignInRejectsUnverifiableCallback(t *testing.T) {
	for _, mode := range []string{"missing-callback", "unsafe-callback"} {
		t.Run(mode, func(t *testing.T) {
			r := loginPipe(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, _, err := r.beginSignIn(ctx, domain.SignInBrowser); err == nil {
				t.Fatal("unverifiable callback accepted")
			}
		})
	}
}

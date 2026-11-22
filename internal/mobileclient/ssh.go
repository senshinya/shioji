package mobileclient

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

const DefaultWorkerPath = "/Applications/Shioji.app/Contents/MacOS/shioji-worker"

type Endpoint struct {
	Address         string `json:"address"`
	User            string `json:"user"`
	HostFingerprint string `json:"hostFingerprint"`
	WorkerPath      string `json:"workerPath"`
	StateDirectory  string `json:"stateDirectory,omitempty"`
	DeviceID        string `json:"deviceID,omitempty"`
	TailnetPeerID   string `json:"tailnetPeerID,omitempty"`
}

func (e Endpoint) validateAddress() error {
	host, port, err := net.SplitHostPort(e.Address)
	number, portErr := strconv.Atoi(port)
	if err != nil || strings.TrimSpace(host) == "" || portErr != nil || number < 1 || number > 65535 {
		return errors.New("请输入主机与端口，例如 mac.local:22")
	}
	if strings.TrimSpace(e.User) == "" || strings.ContainsAny(e.User, "\x00\r\n") {
		return errors.New("请输入设备上的 SSH 用户名")
	}
	return nil
}

func (e Endpoint) Validate() error {
	if err := e.validateAddress(); err != nil {
		return err
	}
	value, ok := strings.CutPrefix(e.HostFingerprint, "SHA256:")
	hash, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if !ok || err != nil || len(hash) != 32 {
		return errors.New("请输入从设备核对的 SHA256 主机指纹")
	}
	if !path.IsAbs(e.WorkerPath) || strings.ContainsAny(e.WorkerPath, "\x00\r\n") {
		return errors.New("请输入设备上执行服务的绝对路径")
	}
	if e.StateDirectory != "" && (!path.IsAbs(e.StateDirectory) || strings.ContainsAny(e.StateDirectory, "\x00\r\n")) {
		return errors.New("执行服务状态目录无效")
	}
	return nil
}

func (e Endpoint) verifyHostKey(_ string, _ net.Addr, key ssh.PublicKey) error {
	if actual := ssh.FingerprintSHA256(key); actual != e.HostFingerprint {
		return fmt.Errorf("SSH 主机指纹与保存的指纹不符（设备返回 %s）", actual)
	}
	return nil
}

func (e Endpoint) command() string {
	remote := quote(e.WorkerPath) + " --bridge"
	if e.StateDirectory != "" {
		remote += " --state-dir " + quote(e.StateDirectory)
	}
	return remote
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Dial presents a remote worker bridge as a deadline-capable protocol stream.
// Each call opens its own authenticated channel, including the event watch.
func (e Endpoint) Dial(ctx context.Context, signer ssh.Signer) (net.Conn, error) {
	return e.DialRoute(ctx, signer, nil)
}

// RouteDial optionally routes a saved address through its owning userspace node.
type RouteDial func(context.Context, string, string) (net.Conn, error)

func (e Endpoint) open(ctx context.Context, route RouteDial) (net.Conn, error) {
	if e.TailnetPeerID != "" {
		if route == nil {
			return nil, errors.New("Tailnet 网络尚未连接")
		}
		return route(ctx, e.TailnetPeerID, e.Address)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", e.Address)
}

func (e Endpoint) InspectHost(ctx context.Context, route RouteDial) (string, error) {
	key, err := e.InspectHostKey(ctx, route)
	return key.Fingerprint, err
}

type HostKeyInfo struct {
	Fingerprint string
	Type        string
}

func (e Endpoint) InspectHostKey(ctx context.Context, route RouteDial) (HostKeyInfo, error) {
	if err := e.validateAddress(); err != nil {
		return HostKeyInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	raw, err := e.open(ctx, route)
	if err != nil {
		return HostKeyInfo{}, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	var info HostKeyInfo
	inspected := errors.New("host key inspected before authentication")
	conn, _, _, err := ssh.NewClientConn(raw, e.Address, &ssh.ClientConfig{
		User: e.User,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			info = HostKeyInfo{Fingerprint: ssh.FingerprintSHA256(key), Type: key.Type()}
			return inspected
		},
	})
	if err != nil && errors.Is(err, inspected) {
		return info, nil
	}
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		err = errors.New("未读取到 SSH 主机指纹")
	}
	return HostKeyInfo{}, err
}

func (e Endpoint) DialRoute(ctx context.Context, signer ssh.Signer, route RouteDial) (net.Conn, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, stop, err := e.authenticate(ctx, signer, route)
	if err != nil {
		return nil, err
	}
	defer stop()
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	stream, err := startBridge(client, session, e.command())
	if err != nil {
		client.Close()
		return nil, err
	}
	if !stop() {
		client.Close()
		return nil, ctx.Err()
	}
	return stream, nil
}

func (e Endpoint) authenticate(ctx context.Context, signer ssh.Signer, route RouteDial) (*ssh.Client, func() bool, error) {
	raw, err := e.open(ctx, route)
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	conn, channels, requests, err := ssh.NewClientConn(raw, e.Address, &ssh.ClientConfig{
		User: e.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: e.verifyHostKey,
	})
	if err != nil {
		stop()
		raw.Close()
		return nil, nil, err
	}
	return ssh.NewClient(conn, channels, requests), stop, nil
}

// ForwardAgentCallback binds only the requested loopback endpoint and opens
// SSH direct-tcpip streams on its pinned, already enrolled route.
func (e Endpoint) ForwardAgentCallback(ctx context.Context, signer ssh.Signer, route RouteDial, callback domain.LoopbackEndpoint) (*transport.LoopbackForward, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return transport.ForwardLoopback(ctx, callback, func(ctx context.Context) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		client, stop, err := e.authenticate(ctx, signer, route)
		if err != nil {
			return nil, err
		}
		defer stop()
		address := net.JoinHostPort(callback.Host, strconv.Itoa(callback.Port))
		conn, err := client.DialContext(ctx, "tcp", address)
		if err != nil {
			client.Close()
			return nil, err
		}
		if !stop() {
			client.Close()
			return nil, ctx.Err()
		}
		return &sshCallbackStream{Conn: conn, client: client}, nil
	})
}

type sshCallbackStream struct {
	net.Conn
	client *ssh.Client
}

func (s *sshCallbackStream) Close() error { return s.client.Close() }

// Bootstrap checks a pinned SSH login for its existing Shioji worker. A route
// for a known device can only connect to that identity and cannot install a new one.
func (e Endpoint) Bootstrap(ctx context.Context, signer ssh.Signer, route RouteDial, artifact transport.WorkerArtifact) (Endpoint, transport.WorkerInfo, error) {
	if err := e.Validate(); err != nil {
		return e, transport.WorkerInfo{}, err
	}
	if e.DeviceID != "" {
		artifact = nil
	}
	remote := e.WorkerSSH(signer, route)
	info, err := transport.BootstrapWorker(ctx, e.WorkerPath, e.StateDirectory, remote.Run, artifact)
	if err != nil {
		return e.legacyBootstrap(info, err)
	}
	if e.DeviceID != "" && e.DeviceID != info.DeviceID {
		return e, info, errors.New("此连接路径指向其他 Shioji 执行设备")
	}
	e.WorkerPath, e.StateDirectory = info.Worker, info.Directory
	if err = e.Validate(); err != nil {
		return e, info, err
	}
	if info.Legacy != nil || info.Migration != nil {
		if info.Legacy != nil && info.Legacy.DeviceID != info.DeviceID ||
			info.Migration != nil && (info.Migration.DeviceID != info.DeviceID || info.Migration.ID == "") {
			return e, info, errors.New("迁移记录不属于指定执行设备")
		}
		e.DeviceID = info.DeviceID
		return e, info, nil
	}
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return e.DialRoute(ctx, signer, route)
	}, info.DeviceID)
	if err != nil {
		return e.legacyBootstrap(info, err)
	}
	e.DeviceID = client.DeviceID
	info.Minor, info.Build = client.Minor, client.Worker
	client.Close()
	return e, info, nil
}

func (e Endpoint) legacyBootstrap(info transport.WorkerInfo, err error) (Endpoint, transport.WorkerInfo, error) {
	var legacy *protocol.LegacyWorkerError
	if !errors.As(err, &legacy) {
		return e, info, err
	}
	if e.DeviceID != "" && legacy.DeviceID != e.DeviceID || info.DeviceID != "" && legacy.DeviceID != info.DeviceID {
		return e, info, errors.New("此连接路径指向其他 Shioji 执行设备")
	}
	e.DeviceID, info.DeviceID = legacy.DeviceID, legacy.DeviceID
	info.Legacy = &domain.LegacyWorkerInfo{DeviceID: legacy.DeviceID, Major: legacy.Major, Minor: legacy.Minor}
	return e, info, nil
}

func (e Endpoint) WorkerSSH(signer ssh.Signer, route RouteDial) transport.RemoteWorker {
	return transport.RemoteWorker{Directory: e.StateDirectory, Worker: e.WorkerPath, Run: func(ctx context.Context, command string, input io.Reader, output, diagnostics io.Writer) (int, error) {
		if err := e.Validate(); err != nil {
			return -1, err
		}
		client, stop, err := e.authenticate(ctx, signer, route)
		if err != nil {
			return -1, err
		}
		defer stop()
		defer client.Close()
		session, err := client.NewSession()
		if err != nil {
			return -1, err
		}
		defer session.Close()
		session.Stdin, session.Stdout, session.Stderr = input, output, diagnostics
		err = session.Run(command)
		if err == nil {
			return 0, nil
		}
		var status *ssh.ExitError
		if errors.As(err, &status) {
			return status.ExitStatus(), err
		}
		return -1, err
	}}
}

func startBridge(client *ssh.Client, session *ssh.Session, command string) (net.Conn, error) {
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	session.Stderr = io.Discard
	if err := session.Start(command); err != nil {
		return nil, err
	}
	local, remote := net.Pipe()
	go func() {
		defer client.Close()
		_, _ = io.Copy(stdin, remote)
	}()
	go func() {
		defer remote.Close()
		_, _ = io.Copy(remote, stdout)
		_ = session.Wait()
	}()
	return local, nil
}

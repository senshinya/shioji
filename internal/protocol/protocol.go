// Package protocol implements the versioned Shioji worker byte-stream protocol.
package protocol

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"shioji/internal/domain"
)

// Major 2 requires clients to understand authoritative history replacement.
const Major = 2

// Minor 21 adds system-owned API setup and public connection summaries.
// Minor 20 adds explicit loopback callback metadata for remote browser authorization.
// Minor 22 adds owning-device tool service discovery and authorization.
// Minor 23 adds device-owned directory browsing before workspace creation.
// Minor 24 adds revision-checked native tool service configuration writes.
// Minor 25 adds scope-aware owning-device Skill inventory, content and enablement.
// Minor 26 adds read-only workspace and existing-session tool inspection.
// Minor 27 adds independent session work observations and read-only details.
// Minor 28 adds durable directed child input and receipt inspection.
// Minor 29 adds durable, explicitly separate context maintenance commands.
// Minor 30 persists user-side command records alongside context maintenance.
// Minor 31 adds device-owned storage measurement and bounded disposable cleanup.
// Minor 32 adds scoped source history discovery, preview and durable import.
// Minor 34 adds read-only device latency probing without waking runtime work.
// Minor 35 adds session-scoped read-only file link resolution and chunks.
// Minor 36 adds bounded display history, prompt indexes and scoped search.
// Minor 37 adds user-message rounds and coherent byte-chunked read windows.
// Minor 38 adds device-owned load telemetry and revision-checked idle-sleep control.
// Minor 39 adds worker-owned reply completion acknowledgements and snapshot time.
// Minor 40 adds device-owned archive policy and permanent archive deletion.
// Minor 41 adds round-based, snapshot-coherent child-agent history paging.
// WorkerBuild.Maintenance advertises durable maintenance independently of minor.
// Minor 42 separates complete conversation imports from bounded previews and
// adds explicit, revision-checked completion of legacy partial imports.
// Minor 43 adds explicit force-close previews and independent device recovery.
// Minor 44 adds explicit normal retirement and forced archive deletion.
// Minor 45 adds durable session runtime reload without submitting input.
// Minor 46 adds scoped Hook management and durable definition-bound receipts.
// Minor 47 adds immutable binary uploads used as owning-device path inputs.
// Minor 48 delegates background unregister to the independent supervisor.
// Minor 49 adds atomic Hook approval of multiple reviewed definitions.
// Minor 50 adds device-owned schedules and durable run receipts.
// Minor 51 adds owning-worker goals and durable goal control receipts.
// Minor 52 adds coherent paged snapshots and a terminal receipt for unreceived input.
// Minor 53 adds bounded catalog projections and batched operation lookup.
const Minor = 53

const CatalogMinor = 53

const RecoveryMinor = 52

const GoalMinor = 51

const HookBatchMinor = 49
const ScheduleMinor = 50

type BackgroundStopInput struct {
	ID string `json:"id"`
}

const MaxFrame = 1 << 20

type Frame struct {
	TCPStreamVersion int                 `json:"tcpStreamVersion,omitempty"`
	Worker           *domain.WorkerBuild `json:"worker,omitempty"`
	Type             string              `json:"type"`
	Major            int                 `json:"major,omitempty"`
	Minor            int                 `json:"minor,omitempty"`
	Nonce            string              `json:"nonce,omitempty"`
	DeviceID         string              `json:"deviceId,omitempty"`
	Generation       string              `json:"generation,omitempty"`
	Epoch            string              `json:"epoch,omitempty"`
	ID               string              `json:"id,omitempty"`
	Method           string              `json:"method,omitempty"`
	Body             json.RawMessage     `json:"body,omitempty"`
	ErrorCode        string              `json:"errorCode,omitempty"`
	Error            string              `json:"error,omitempty"`
}
type Codec struct {
	r io.Reader
	w io.Writer
}

func NewCodec(r io.Reader, w io.Writer) *Codec { return &Codec{r: r, w: w} }
func (c *Codec) Read(f *Frame) error {
	var head [4]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > MaxFrame {
		return fmt.Errorf("invalid frame length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return err
	}
	if err := json.Unmarshal(b, f); err != nil {
		return err
	}
	if f.Type == "" {
		return errors.New("missing frame type")
	}
	return nil
}
func (c *Codec) Write(f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(b) > MaxFrame {
		return errors.New("frame exceeds 1 MiB")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if err = writeAll(c.w, h[:]); err != nil {
		return err
	}
	return writeAll(c.w, b)
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

type Client struct {
	TCPStreamVersion int
	Worker           *domain.WorkerBuild
	mu               sync.Mutex
	conn             net.Conn
	codec            *Codec
	DeviceID, Epoch  string
	Generation       string
	Minor            int
}

// LegacyWorkerError identifies an authenticated endpoint which predates the
// current protocol. Its diagnostic probe never sends execution or update commands.
type LegacyWorkerError struct {
	Major, Minor int
	DeviceID     string
}

func (e *LegacyWorkerError) Error() string {
	return fmt.Sprintf("设备执行服务版本过旧（协议 %d.%d），无法安全自动更新。请先停止旧版服务，再重试连接", e.Major, e.Minor)
}

// Dial owns failed connections. A v1 probe after a closed handshake is diagnostic
// only: it cannot turn an incompatible worker into an executable client.
func Dial(ctx context.Context, dial func(context.Context) (net.Conn, error), target string) (*Client, error) {
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	client, err := Connect(ctx, conn, target)
	if err == nil {
		return client, nil
	}
	conn.Close()
	if errors.Is(err, io.EOF) && ctx.Err() == nil {
		if old, probeErr := dial(ctx); probeErr == nil {
			defer old.Close()
			reset := deadline(ctx, old)
			defer reset()
			codec, nonce := NewCodec(old, old), domain.NewID()
			if codec.Write(Frame{Type: "hello", Major: 1, Nonce: nonce, DeviceID: target}) == nil {
				var welcome Frame
				if codec.Read(&welcome) == nil && welcome.Type == "welcome" && welcome.Major == 1 && welcome.Nonce == nonce && welcome.DeviceID != "" && welcome.Epoch != "" {
					if target != "" && welcome.DeviceID != target {
						return nil, errors.New("设备身份与已保存记录不一致")
					}
					return nil, &LegacyWorkerError{Major: welcome.Major, Minor: welcome.Minor, DeviceID: welcome.DeviceID}
				}
			}
		}
	}
	return nil, fmt.Errorf("执行服务连接或握手失败：%w", err)
}

func Connect(ctx context.Context, conn net.Conn, target string) (*Client, error) {
	client := &Client{conn: conn, codec: NewCodec(conn, conn)}
	reset := deadline(ctx, conn)
	defer reset()
	nonce := domain.NewID()
	if err := client.codec.Write(Frame{Type: "hello", Major: Major, Nonce: nonce, DeviceID: target}); err != nil {
		return nil, err
	}
	var f Frame
	if err := client.codec.Read(&f); err != nil {
		return nil, err
	}
	if f.Type != "welcome" || f.Major != Major || f.Nonce != nonce || f.DeviceID == "" || f.Epoch == "" {
		return nil, errors.New("incompatible or invalid worker handshake")
	}
	if target != "" && target != f.DeviceID {
		return nil, errors.New("worker device identity changed")
	}
	client.DeviceID = f.DeviceID
	client.Epoch = f.Epoch
	client.Generation = f.Generation
	client.Minor = f.Minor
	client.TCPStreamVersion = f.TCPStreamVersion
	client.Worker = f.Worker
	return client, nil
}
func (c *Client) Close() error { return c.conn.Close() }
func deadline(ctx context.Context, c net.Conn) func() {
	d, ok := ctx.Deadline()
	if !ok {
		d = time.Now().Add(15 * time.Second)
	}
	c.SetDeadline(d)
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Now()) })
	return func() { stop(); c.SetDeadline(time.Time{}) }
}
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	reset := deadline(ctx, c.conn)
	defer reset()
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	id := domain.NewID()
	if err = c.codec.Write(Frame{Type: "command", ID: id, Method: method, DeviceID: c.DeviceID, Epoch: c.Epoch, Body: b}); err != nil {
		return err
	}
	var f Frame
	if err = c.codec.Read(&f); err != nil {
		return err
	}
	if f.ID != id || f.DeviceID != c.DeviceID || f.Epoch != c.Epoch {
		return errors.New("response identity mismatch")
	}
	if f.Error != "" {
		return &RemoteError{Code: f.ErrorCode, Message: f.Error}
	}
	if f.Type != "result" {
		return errors.New("unexpected response")
	}
	if out != nil {
		return json.Unmarshal(f.Body, out)
	}
	return nil
}

// Welcome verifies a fresh connection. Authorization of the underlying transport precedes this.
func Welcome(conn net.Conn, deviceID string, build ...domain.WorkerBuild) (*Codec, string, error) {
	return welcome(conn, deviceID, "", build)
}

// WelcomeWorker carries the worker's journal generation independently of the
// command connection epoch. Older peers can continue to verify it by snapshot.
func WelcomeWorker(conn net.Conn, deviceID, generation string, build domain.WorkerBuild) (*Codec, string, error) {
	return welcome(conn, deviceID, generation, []domain.WorkerBuild{build})
}

func welcome(conn net.Conn, deviceID, generation string, build []domain.WorkerBuild) (*Codec, string, error) {
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})
	c := NewCodec(conn, conn)
	var hello Frame
	if err := c.Read(&hello); err != nil {
		return nil, "", err
	}
	if hello.Type != "hello" || hello.Major != Major || hello.Nonce == "" || (hello.DeviceID != "" && hello.DeviceID != deviceID) {
		return nil, "", errors.New("handshake rejected")
	}
	epoch := domain.NewID()
	welcome := Frame{TCPStreamVersion: TCPStreamVersion, Type: "welcome", Major: Major, Minor: Minor, Nonce: hello.Nonce, DeviceID: deviceID, Epoch: epoch, Generation: generation}
	if len(build) > 0 {
		welcome.Worker = &build[0]
	}
	err := c.Write(welcome)
	return c, epoch, err
}

// ReadinessQuery scopes live runtime profiles to the client's two visible sessions.
type ReadinessQuery struct {
	SessionIDs []string `json:"sessionIds,omitempty"`
}

type OperationQuery struct {
	ID             string `json:"id"`
	SealGeneration string `json:"sealGeneration,omitempty"`
}

type OperationBatchQuery struct {
	Operations []OperationQuery `json:"operations"`
}

type CompletionReadInput struct {
	SessionID    string `json:"sessionId"`
	CompletionID string `json:"completionId"`
}

type WorkerMaintenanceQuery struct {
	DeviceID string `json:"deviceId"`
	ID       string `json:"id,omitempty"`
}

type DirectoryQuery struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Empty Executable restores automatic discovery on the owning device.
type RuntimeConfiguration struct {
	Options    *domain.RuntimeOptions `json:"options,omitempty"`
	HarnessID  string                 `json:"harnessId"`
	Executable string                 `json:"executable"`
}

// ID identifies a single explicitly started authorization. Querying an unknown
// ID must never restart it, including after worker restart or network loss.
type SignInInput struct {
	API           *domain.APIConfiguration `json:"api,omitempty"`
	ID            string                   `json:"id"`
	HarnessID     string                   `json:"harnessId"`
	Method        domain.SignInMethod      `json:"method,omitempty"`
	Configuration *RuntimeConfiguration    `json:"configuration,omitempty"`
}

// DeviceAgents is the complete set selected for one owning device.
type DeviceAgents struct {
	Agents []RuntimeConfiguration `json:"agents"`
}
type AgentSetupResult struct {
	Readiness domain.Readiness     `json:"readiness"`
	Catalog   []domain.HarnessInfo `json:"catalog"`
	Ready     bool                 `json:"ready"`
	Error     string               `json:"error,omitempty"`
}

// ToolServiceInput selects an opaque owning-harness service. ID identifies one
// explicit authorization attempt and is never used to replay a lost start.
type ToolServiceInput struct {
	Change    *domain.ToolServiceChange `json:"change,omitempty"`
	HarnessID string                    `json:"harnessId"`
	ServiceID string                    `json:"serviceId,omitempty"`
	ID        string                    `json:"id,omitempty"`
}

type ToolInspectionInput struct {
	WorkspaceID string `json:"workspaceId"`
	HarnessID   string `json:"harnessId"`
	Directory   string `json:"directory,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
}

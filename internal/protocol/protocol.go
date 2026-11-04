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

const Major = 1
const MaxFrame = 1 << 20

type Frame struct {
	Type      string          `json:"type"`
	Major     int             `json:"major,omitempty"`
	Minor     int             `json:"minor,omitempty"`
	Nonce     string          `json:"nonce,omitempty"`
	DeviceID  string          `json:"deviceId,omitempty"`
	Epoch     string          `json:"epoch,omitempty"`
	ID        string          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
	ErrorCode string          `json:"errorCode,omitempty"`
	Error     string          `json:"error,omitempty"`
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
	mu              sync.Mutex
	conn            net.Conn
	codec           *Codec
	DeviceID, Epoch string
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
func Welcome(conn net.Conn, deviceID string) (*Codec, string, error) {
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
	err := c.Write(Frame{Type: "welcome", Major: Major, Nonce: hello.Nonce, DeviceID: deviceID, Epoch: epoch})
	return c, epoch, err
}

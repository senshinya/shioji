package protocol_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"shioji/internal/protocol"
	"strings"
	"testing"
)

func TestCodecRejectsOversizedAndTruncatedFrames(t *testing.T) {
	for _, payload := range [][]byte{{0, 16, 0, 1}, {0, 0, 0, 5, '{', '}'}, {0, 0, 0, 0}} {
		var f protocol.Frame
		if err := protocol.NewCodec(bytes.NewReader(payload), &bytes.Buffer{}).Read(&f); err == nil {
			t.Fatalf("accepted malformed frame %v", payload)
		}
	}
	var b bytes.Buffer
	if err := protocol.NewCodec(&b, &b).Write(protocol.Frame{Type: "query", ID: "one", Method: "snapshot"}); err != nil {
		t.Fatal(err)
	}
	if n := binary.BigEndian.Uint32(b.Bytes()[:4]); n != uint32(b.Len()-4) {
		t.Fatal("wrong frame length")
	}
	var f protocol.Frame
	if err := protocol.NewCodec(&b, &b).Read(&f); err != nil || f.ID != "one" {
		t.Fatalf("roundtrip: %+v %v", f, err)
	}
}
func TestHandshakeRejectsWrongVersionDeviceAndNonce(t *testing.T) {
	for _, which := range []string{"major", "device", "nonce"} {
		t.Run(which, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			go func() {
				c := protocol.NewCodec(b, b)
				var f protocol.Frame
				c.Read(&f)
				f.Type = "welcome"
				f.DeviceID = "device-b"
				f.Epoch = "epoch"
				if which == "major" {
					f.Major = 99
				}
				if which == "nonce" {
					f.Nonce = "wrong"
				}
				c.Write(f)
			}()
			target := "device-b"
			if which == "device" {
				target = "device-a"
			}
			if _, err := protocol.Connect(context.Background(), a, target); err == nil {
				t.Fatal("invalid welcome accepted")
			}
		})
	}
}
func TestClientBindsEveryCallToWelcomeEpoch(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		c := protocol.NewCodec(b, b)
		var hello protocol.Frame
		c.Read(&hello)
		hello.Type = "welcome"
		hello.DeviceID = "device"
		hello.Epoch = "epoch-1"
		c.Write(hello)
		var f protocol.Frame
		err := c.Read(&f)
		if f.Epoch != "epoch-1" || f.DeviceID != "device" || f.Method != "snapshot" {
			err = net.ErrClosed
		}
		f.Type = "result"
		f.Body = []byte(`{"ok":true}`)
		c.Write(f)
		done <- err
	}()
	client, err := protocol.Connect(context.Background(), a, "device")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err = client.Call(context.Background(), "snapshot", nil, &out); err != nil || !out.OK {
		t.Fatalf("call: %v %+v", err, out)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestCodecRejectsOversizedWrites(t *testing.T) {
	var out bytes.Buffer
	err := protocol.NewCodec(&out, &out).Write(protocol.Frame{Type: strings.Repeat("x", protocol.MaxFrame)})
	if err == nil || out.Len() != 0 {
		t.Fatal("oversized write reached transport")
	}
}

func TestWelcomeRetainsActualWorkerBuildWithoutRequiringLegacyMetadata(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			codec := protocol.NewCodec(b, b)
			var hello protocol.Frame
			if codec.Read(&hello) != nil {
				return
			}
			f := map[string]any{"type": "welcome", "major": protocol.Major, "minor": protocol.Minor, "nonce": hello.Nonce, "deviceId": "device", "epoch": "epoch"}
			if metadata {
				f["worker"] = map[string]any{"buildId": "running-build", "revision": "revision", "builtAt": 123, "safeUpdate": true}
			}
			data, _ := json.Marshal(f)
			binary.Write(b, binary.BigEndian, uint32(len(data)))
			b.Write(data)
		}()
		client, err := protocol.Connect(context.Background(), a, "device")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(client)
		var fields map[string]json.RawMessage
		json.Unmarshal(data, &fields)
		if metadata && !bytes.Contains(fields["Worker"], []byte("running-build")) {
			t.Fatal("welcome lost the actual worker build", string(data))
		}
		client.Close()
	}
}

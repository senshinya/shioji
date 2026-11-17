package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPathAttachmentTransferProgressAndLegacyGate(t *testing.T) {
	for _, mode := range []string{"complete", "cancel", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			stage, remote := attachments.New(t.TempDir()), attachments.New(t.TempDir())
			data := bytes.Repeat([]byte{0, 0xff, 1, 2}, 40000)
			a, err := stage.SavePathResource("archive.zip", data)
			if err != nil {
				t.Fatal(err)
			}
			workspace := domain.NewID()
			var chunks atomic.Int32
			done := make(chan struct{})
			target := AttachmentTarget{DeviceID: "peer", WorkspaceID: workspace, dial: func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer close(done)
					defer server.Close()
					codec := protocol.NewCodec(server, server)
					var hello protocol.Frame
					if codec.Read(&hello) != nil {
						return
					}
					minor := protocol.Minor
					if mode == "legacy" {
						minor = 46
					}
					if codec.Write(protocol.Frame{Type: "welcome", Major: protocol.Major, Minor: minor, Nonce: hello.Nonce, DeviceID: "peer", Epoch: "epoch"}) != nil {
						return
					}
					for {
						var frame protocol.Frame
						if codec.Read(&frame) != nil {
							return
						}
						chunks.Add(1)
						var in protocol.AttachmentPut
						if frame.Method != "attachment.put" || json.Unmarshal(frame.Body, &in) != nil || in.WorkspaceID != workspace || in.Attachment != a {
							t.Error("wrong resource or owner", frame.Method)
							return
						}
						receipt, err := remote.Put(workspace, in.Attachment, in.Offset, in.Data)
						if err != nil {
							t.Error(err)
							return
						}
						result(codec, frame, receipt)
					}
				}()
				return client, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var progress []int64
			err = target.UploadProgress(ctx, stage, a, func(offset int64) {
				progress = append(progress, offset)
				if mode == "cancel" {
					cancel()
				}
			})
			<-done
			switch mode {
			case "complete":
				if err != nil || len(progress) != 3 || progress[0] != 65536 || progress[1] != 131072 || progress[2] != 160000 {
					t.Fatal("progress is not acknowledged byte counts", progress, err)
				}
			case "cancel":
				if err == nil || chunks.Load() != 1 {
					t.Fatal("cancelled upload sent more bytes", chunks.Load(), err)
				}
			case "legacy":
				if err == nil || !strings.Contains(err.Error(), "升级") || chunks.Load() != 0 || len(progress) != 0 {
					t.Fatal("legacy worker received a binary upload", chunks.Load(), err)
				}
			}
		})
	}
}

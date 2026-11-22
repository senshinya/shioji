package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func TestNativeBackgroundStopObservesLostReplyBeforeDetachedHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	control, server := net.Pipe()
	defer control.Close()
	defer server.Close()
	build := domain.WorkerBuild{Managed: true}
	accepted := make(chan string, 1)
	go func() {
		defer server.Close()
		codec, _, err := protocol.WelcomeWorker(server, "device-a", "generation-a", build)
		if err != nil {
			return
		}
		var frame protocol.Frame
		if codec.Read(&frame) != nil || frame.Method != "worker.background.stop" || frame.DeviceID != "device-a" {
			return
		}
		var input protocol.BackgroundStopInput
		if json.Unmarshal(frame.Body, &input) == nil {
			accepted <- input.ID
		}
		// The owner accepted the command before losing its business connection.
	}()
	client, err := protocol.Connect(ctx, control, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	receiptRead, detached := false, false
	remote := transport.RemoteWorker{Worker: "/Shioji.app/Contents/MacOS/shioji-worker",
		Run: func(_ context.Context, command string, in io.Reader, out, _ io.Writer) (int, error) {
			if in != nil {
				t.Fatal("handoff must not upload any artifact")
			}
			switch {
			case strings.Contains(command, "background-status"):
				select {
				case id := <-accepted:
					if receiptRead || id == "" || !strings.Contains(command, id) || !strings.Contains(command, "device-a") {
						t.Fatal("missing original action or authenticated device in receipt query", command)
					}
				default:
					t.Fatal("the worker did not accept the original Stop")
				}
				receiptRead = true
				io.WriteString(out, `{"background":{"format":1,"state":"notRegistered"}}`)
			case strings.Contains(command, "--ensure --state-dir"):
				if !receiptRead || detached {
					t.Fatal("detached owner requested without a terminal receipt")
				}
				detached = true
				json.NewEncoder(out).Encode(transport.WorkerInfo{DeviceID: "device-a",
					Worker: "/Shioji.app/Contents/MacOS/shioji-worker", Directory: "/private/worker", Build: &domain.WorkerBuild{}})
			default:
				t.Fatal("native unregister was replayed", command)
			}
			return 0, nil
		}}
	state, err := (&Controller{}).changeWorkerBackground(ctx, client, &build, remote, nil, "unregister")
	if err != nil || state.State != "notRegistered" || !receiptRead || !detached {
		t.Fatalf("handoff did not settle: %+v, %v", state, err)
	}
}

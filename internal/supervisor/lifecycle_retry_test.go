package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"shioji/internal/domain"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleTestPair(t *testing.T) (*lifecycleClient, net.Conn) {
	t.Helper()
	client, inherited, err := lifecyclePair()
	if err != nil {
		t.Fatal(err)
	}
	server, err := net.FileConn(inherited)
	inherited.Close()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}
func TestPrivateLifecycleTimeoutKeepsControlAvailable(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "responsive-rejection"
		if timeout {
			name = "timed-out-rejection"
		}
		t.Run(name, func(t *testing.T) {
			client, server := lifecycleTestPair(t)
			entered, release, handled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			go func() {
				defer server.Close()
				ServeLifecycle(server, func(in Control) (domain.WorkerUpdateResult, error) {
					if calls.Add(1) == 1 {
						close(entered)
						if timeout {
							<-release
						}
						close(handled)
						return domain.WorkerUpdateResult{}, errors.New("first rejected")
					}
					return domain.WorkerUpdateResult{Ready: true, OperationID: in.OperationID}, nil
				})
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			first := make(chan error, 1)
			go func() { _, err := client.request(ctx, Control{Instance: "same", OperationID: "first"}); first <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("first request not delivered")
			}
			select {
			case err := <-first:
				if err == nil {
					t.Fatal("first attempt should reject")
				}
			case <-time.After(time.Second):
				t.Fatal("request ignored its deadline")
			}
			if timeout {
				close(release)
			}
			<-handled
			next, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			result, err := client.request(next, Control{Instance: "same", OperationID: "retry"})
			if err != nil || !result.Ready || result.OperationID != "retry" {
				t.Fatal("one unaccepted request destroyed later control", result, err)
			}
			if calls.Load() != 2 {
				t.Fatal("retry never reached same live peer", calls.Load())
			}
		})
	}
}
func TestPrivateLifecycleLateReplyCannotSettleNewAttempt(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint("partial=", partial), func(t *testing.T) {
			client, server := lifecycleTestPair(t)
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- func() error {
					decode := json.NewDecoder(server)
					var first map[string]any
					if err := decode.Decode(&first); err != nil {
						return err
					}
					raw, err := json.Marshal(map[string]any{"attemptId": first["attemptId"], "result": domain.WorkerUpdateResult{Ready: true, OperationID: "old-attempt"}})
					if err != nil {
						return err
					}
					raw = append(raw, '\n')
					if partial {
						if _, err = server.Write(raw[:13]); err != nil {
							return err
						}
						raw = raw[13:]
					}
					<-release
					if _, err = server.Write(raw); err != nil {
						return err
					}
					var second map[string]any
					if err = decode.Decode(&second); err != nil {
						return err
					}
					return json.NewEncoder(server).Encode(map[string]any{"attemptId": second["attemptId"], "result": domain.WorkerUpdateResult{Ready: false, Reason: "new attempt busy"}})
				}()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, err := client.request(ctx, Control{Instance: "same", OperationID: "old-attempt"})
			cancel()
			if err == nil {
				t.Fatal("delayed old reply should outlive first caller")
			}
			close(release)
			next, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			result, err := client.request(next, Control{Instance: "same", OperationID: "new-attempt"})
			if err != nil || result.Ready || result.Reason != "new attempt busy" {
				t.Fatal("stale/partial reply was lost or authorized a new attempt", result, err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPrivateLifecycleRejectsOversizedReply(t *testing.T) {
	client, server := lifecycleTestPair(t)
	go func() {
		var in Control
		if json.NewDecoder(server).Decode(&in) == nil {
			server.Write([]byte(strings.Repeat("x", lifecycleFrameLimit)))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.request(ctx, Control{OperationID: "malformed"}); err == nil {
		t.Fatal("oversized unterminated response accepted")
	}
}

func TestPrivateLifecyclePartialWriteSurvivesCallerTimeout(t *testing.T) {
	conn, server := net.Pipe()
	client := newLifecycleClient(conn)
	t.Cleanup(func() { client.Close(); server.Close() })
	release, done := make(chan struct{}), make(chan error, 1)
	go func() {
		prefix := make([]byte, 7)
		if _, err := io.ReadFull(server, prefix); err != nil {
			done <- err
			return
		}
		<-release
		decode := json.NewDecoder(io.MultiReader(bytes.NewReader(prefix), server))
		var first, second map[string]any
		if err := decode.Decode(&first); err != nil {
			done <- err
			return
		}
		if err := decode.Decode(&second); err != nil {
			done <- err
			return
		}
		done <- json.NewEncoder(server).Encode(map[string]any{"attemptId": second["attemptId"], "result": domain.WorkerUpdateResult{Ready: true, OperationID: "second"}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err := client.request(ctx, Control{OperationID: "first"})
	cancel()
	if err == nil {
		t.Fatal("blocked write ignored request deadline")
	}
	close(release)
	next, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := client.request(next, Control{OperationID: "second"})
	if err != nil || !result.Ready || result.OperationID != "second" {
		t.Fatal("partial old frame destroyed new request", result, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPrivateLifecycleWaitingCallerCancellationAndChildExit(t *testing.T) {
	client, server := lifecycleTestPair(t)
	entered := make(chan struct{})
	go func() {
		var in Control
		if json.NewDecoder(server).Decode(&in) == nil {
			close(entered)
		}
	}()
	first := make(chan error, 1)
	go func() { _, err := client.request(context.Background(), Control{OperationID: "held"}); first <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not delivered")
	}
	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.request(waiting, Control{OperationID: "canceled-waiter"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting caller did not cancel", err)
	}
	server.Close()
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("child exit accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("child exit did not release caller")
	}
}

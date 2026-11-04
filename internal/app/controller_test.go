package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func peer(t *testing.T, serve func(*protocol.Codec, string)) *protocol.Client {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go func() {
		codec, epoch, err := protocol.Welcome(b, "device")
		if err == nil {
			serve(codec, epoch)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := protocol.Connect(ctx, a, "device")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func result(codec *protocol.Codec, request protocol.Frame, body any) {
	b, _ := json.Marshal(body)
	codec.Write(protocol.Frame{Type: "result", ID: request.ID, Epoch: request.Epoch, DeviceID: request.DeviceID, Body: b})
}
func TestStaleQueuedMutationNeverReachesReplacementConnection(t *testing.T) {
	sent := make(chan bool, 1)
	c := peer(t, func(codec *protocol.Codec, _ string) {
		var f protocol.Frame
		if codec.Read(&f) == nil {
			sent <- true
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := executeJob(ctx, c, Job{Method: "session.submit", epoch: "old-connection", Input: domain.Submission{OperationID: "op"}})
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("stale unsent job must be rejected locally: %v", err)
	}
	select {
	case <-sent:
		t.Fatal("mutation reached replacement connection")
	default:
	}
}
func TestDefinitelyUnsentSubmissionDoesNotPermanentlyBlockDraft(t *testing.T) {
	for _, prepare := range []func() error{nil, func() error { return errors.New("disk unavailable") }} {
		m := model()
		m.Drafts["home"] = "keep"
		in, _ := m.BeginSubmission()
		_, err := executeJob(context.Background(), nil, Job{Method: "session.submit", Prepare: prepare})
		m.SubmissionError(in, err)
		if len(m.Pending) != 0 || m.Drafts["home"] != "keep" {
			t.Fatal("definitely unsent draft is stuck")
		}
	}
}
func TestHydrationReplaysFinalizationBetweenPageReads(t *testing.T) {
	c := peer(t, func(codec *protocol.Codec, _ string) {
		for i := 0; i < 3; i++ {
			var f protocol.Frame
			if codec.Read(&f) != nil {
				return
			}
			switch i {
			case 0:
				result(codec, f, domain.SessionDetail{Session: domain.Session{ID: "s"}, Sequence: 1, HasMore: true, Items: []domain.Item{{ID: "first", Order: 1, Text: "partial"}}})
			case 1:
				result(codec, f, domain.SessionDetail{Session: domain.Session{ID: "s"}, Sequence: 2, Items: []domain.Item{{ID: "second", Order: 2, Text: "second"}}})
			case 2:
				var q protocol.EventsQuery
				json.Unmarshal(f.Body, &q)
				if q.After != 1 {
					return
				}
				result(codec, f, protocol.EventPage{Events: []domain.Event{{Sequence: 2, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "first", Order: 1, Text: "authoritative", Final: true}}}})
			}
		}
	})
	detail, err := hydrateSession(context.Background(), c, "s")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Items[0].Text != "authoritative" || !detail.Items[0].Final || detail.Sequence != 2 {
		t.Fatalf("lost page-overlap update: %+v", detail)
	}
}
func TestReconciliationRetainsQueriesThatDidNotReachWorker(t *testing.T) {
	ids := map[string]bool{"op": true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reconcileOperations(ctx, nil, ids, func(Update) {}); err == nil {
		t.Fatal("disconnected reconciliation pretended to finish")
	}
	if !ids["op"] {
		t.Fatal("unconfirmed query was forgotten")
	}
}

func TestQueuedJobRetainsEpochOfTheVisibleUIState(t *testing.T) {
	c := NewController("")
	c.epoch = "replacement"
	if !c.Do(Job{Method: "session.submit", ConnectionEpoch: "visible-old"}) {
		t.Fatal("job rejected before validation")
	}
	job := <-c.Jobs
	if job.epoch != "visible-old" {
		t.Fatal("new connection silently rebound a stale UI action")
	}
}

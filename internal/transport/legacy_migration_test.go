package transport

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestMigrationProgressRejectsForeignReplyAndPreservesAuthoritativeOperation(t *testing.T) {
	in := domain.WorkerMigrationInput{ID: "requested", DeviceID: "device"}
	state := domain.WorkerMigrationStatus{ID: "already-active", DeviceID: "foreign", Phase: domain.MigrationPrepared}
	b, _ := json.Marshal(state)
	if _, e := decodeWorkerMigration(b, in); e == nil {
		t.Fatal("accepted foreign device")
	}
	state.DeviceID = in.DeviceID
	b, _ = json.Marshal(state)
	got, e := decodeWorkerMigration(b, in)
	if e != nil || got.ID != "already-active" {
		t.Fatal(got, e)
	}
	b, _ = json.Marshal(protocol.Frame{Type: "worker-migration-error", ID: in.ID, DeviceID: "foreign", ErrorCode: "notFound", Error: "wrong"})
	if _, e = decodeWorkerMigration(b, in); e == nil || e.Error() == "wrong" {
		t.Fatal("trusted foreign error frame", e)
	}
}

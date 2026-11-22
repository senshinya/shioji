package mobileclient

import (
	"encoding/json"
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func reloadFixture() (*app.Model, domain.Session) {
	m := app.NewModel()
	m.Connected, m.WorkerMinor, m.Snapshot.DeviceID = true, protocol.Minor, "device"
	m.Readiness = domain.Readiness{HarnessID: "codex", Capabilities: domain.HarnessCapabilities{Reload: true}}
	s := domain.Session{ID: "session", WorkspaceID: "work", ThreadID: "thread", HarnessID: "codex", Revision: 7,
		Status: domain.ExecutionCompleted, Context: domain.LaunchContext{DeviceID: "device"}}
	m.Snapshot.Workspaces = []domain.Workspace{{ID: s.WorkspaceID, DeviceID: "device"}}
	m.Details[s.ID] = domain.SessionDetail{Session: s}
	return m, s
}

func TestSessionReloadIntentKeepsOriginalScopeAndReceiptAcrossEncoding(t *testing.T) {
	m, s := reloadFixture()
	intent := SessionReloadIntent{Target: ReloadTargetFor(m.Snapshot.DeviceID, s), Request: domain.SessionReloadRequest{
		OperationID: "original", SessionID: s.ID, Revision: s.Revision,
	}}
	var restored SessionReloadIntent
	data, err := json.Marshal(intent)
	if err != nil || json.Unmarshal(data, &restored) != nil || !restored.Valid() || restored != intent {
		t.Fatalf("could not restore the original request: %+v %v", restored, err)
	}
	receipt := domain.SessionReloadResult{Session: s, Operation: domain.SessionReloadOperation{Input: restored.Request, State: "completed"}}
	if !restored.Accepts(receipt) {
		t.Fatal("original receipt did not match")
	}
	for _, change := range []func(*domain.SessionReloadResult){
		func(r *domain.SessionReloadResult) { r.Session.ID = "other" },
		func(r *domain.SessionReloadResult) { r.Session.Context.DeviceID = "other" },
		func(r *domain.SessionReloadResult) { r.Session.WorkspaceID = "other" },
		func(r *domain.SessionReloadResult) { r.Session.ThreadID = "other" },
		func(r *domain.SessionReloadResult) { r.Operation.Input.OperationID = "other" },
		func(r *domain.SessionReloadResult) { r.Operation.Input.Revision-- },
		func(r *domain.SessionReloadResult) { r.Operation.State = "unexpected" },
	} {
		r := receipt
		change(&r)
		if restored.Accepts(r) {
			t.Fatalf("accepted mismatched receipt: %+v", r)
		}
	}
}

func TestSessionReloadUsesOwningSessionQueueAndPendingInput(t *testing.T) {
	m, s := reloadFixture()
	m.SessionID = "other-reading-session"
	m.Pending["other"] = domain.Submission{SessionID: m.SessionID}
	if reason := SessionReloadReason(m, s); reason != "" {
		t.Fatalf("another session blocked reloading: %s", reason)
	}
	m.Pending["same"] = domain.Submission{SessionID: s.ID}
	if reason := SessionReloadReason(m, s); reason == "" {
		t.Fatal("an input without a receipt did not block reloading")
	}
	delete(m.Pending, "same")
	d := m.Details[s.ID]
	d.Queue.Items = []domain.QueueItem{{ID: "saved"}}
	m.Details[s.ID] = d
	if reason := SessionReloadReason(m, s); reason != "请先暂停待发队列" {
		t.Fatalf("active queue: %s", reason)
	}
	d.Queue.Paused = true
	m.Details[s.ID] = d
	if reason := SessionReloadReason(m, s); reason != "" {
		t.Fatalf("paused queue should be kept: %s", reason)
	}
	m.WorkerMinor = protocol.SessionReloadMinor - 1
	if reason := SessionReloadReason(m, s); reason != "需要更新执行服务" {
		t.Fatalf("old worker: %s", reason)
	}
}

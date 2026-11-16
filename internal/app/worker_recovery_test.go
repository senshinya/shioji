package app

import (
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"testing"
)

func TestExplicitMaintenanceStandaloneAndUnknownReceipt(t *testing.T) {
	m := maintenanceClientModel()
	state := m.DeviceStates["local"]
	state.WorkerService.Build.ForceRecovery = true
	state.WorkerService.Available = nil
	target := domain.WorkerMaintenanceTarget{SessionID: "active", Generation: "g", ProcessRef: "p", Revision: 2}
	m.MaintenanceClient("D").Diagnosis = &domain.WorkerUpdateDiagnosis{DeviceID: "D", BuildID: "old", Environments: []domain.WorkerUpdateBlocker{{SessionID: "active", Status: "running", ForceTarget: &target}}}
	selection := WorkerCloseSelection{Targets: []domain.WorkerMaintenanceTarget{target}, Force: true, RiskAccepted: true}
	job, err := m.CloseWorkerEnvironments("local", selection)
	if err != nil {
		t.Fatal(err)
	}
	in := job.Input.(domain.WorkerMaintenanceInput)
	if in.Action != "force-close" || !in.Standalone || in.BuildID != "" {
		t.Fatal("standalone closure required an update", in)
	}
	job.Apply(m, nil, errors.New("receipt lost"))
	if _, err = m.CloseWorkerEnvironments("local", selection); err == nil {
		t.Fatal("unknown force receipt replayed")
	}
	query, err := m.QueryWorkerMaintenance("local")
	if err != nil || query.Method != "worker.maintenance.get" {
		t.Fatal(query, err)
	}
}

func TestIndependentRecoveryRemainsAvailableDisconnectedAndIsQueryOnlyAfterLoss(t *testing.T) {
	m := maintenanceClientModel()
	m.DeviceStates["local"].Connected = false
	m.DeviceStates["local"].WorkerService = nil
	job, err := m.WorkerRecoveryJob("local", "inspect", domain.WorkerRecoveryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Method != "$worker.recovery" {
		t.Fatal("helper went through dead worker", job)
	}
	preview := domain.WorkerRecoveryPreview{DeviceID: "D", Ref: "preview"}
	b, _ := json.Marshal(preview)
	job.Apply(m, b, nil)
	client := m.RecoveryClient("D")
	if client.Preview == nil {
		t.Fatal("helper preview lost")
	}
	in := domain.WorkerRecoveryInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "restart", DeviceID: "D", PreviewRef: "preview", RiskAccepted: true}
	job, err = m.WorkerRecoveryJob("local", "start", in)
	if err != nil {
		t.Fatal(err)
	}
	job.Apply(m, nil, errors.New("SSH disconnected"))
	if !client.QueryOnly || client.PendingID != in.ID {
		t.Fatal("lost helper receipt cleared identity", client)
	}
	if _, err = m.WorkerRecoveryJob("local", "start", in); err == nil {
		t.Fatal("lost helper action replayed")
	}
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	var saved savedState
	if json.Unmarshal(data, &saved) != nil || saved.WorkerRecoveryIDs["D"] != in.ID {
		t.Fatal("helper query identity not persisted")
	}
	if _, err = m.WorkerRecoveryJob("local", "status", domain.WorkerRecoveryInput{}); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentRecoveryRejectsForeignCallback(t *testing.T) {
	m := maintenanceClientModel()
	job, err := m.WorkerRecoveryJob("local", "inspect", domain.WorkerRecoveryInput{})
	if err != nil {
		t.Fatal(err)
	}
	m.DeviceStates["local"].Snapshot.DeviceID = "replacement"
	b, _ := json.Marshal(domain.WorkerRecoveryPreview{DeviceID: "D", Ref: "old"})
	job.Apply(m, b, nil)
	if m.RecoveryClient("D").Preview != nil {
		t.Fatal("old helper callback crossed device identity")
	}
}

func TestRecoveryWarningsAreDeduplicatedAndBounded(t *testing.T) {
	m := maintenanceClientModel()
	m.Preferences = m.Preferences.Normalized()
	m.Preferences.Notifications.Enabled = true
	m.Preferences.Notifications.Attention = true
	m.queueRecoveryNotice("local", "blocked", "需要处理")
	m.queueRecoveryNotice("local", "blocked", "需要处理")
	if len(m.Notifications) != 1 {
		t.Fatal("warning replay", len(m.Notifications))
	}
	for i := 0; i < 300; i++ {
		m.queueRecoveryNotice("local", string(rune(i))+"event", "需要处理")
	}
	if len(m.Notifications) > 64 || len(m.notificationSeen) > 256 {
		t.Fatal("unbounded recovery notices", len(m.Notifications), len(m.notificationSeen))
	}
}

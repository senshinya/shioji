package app

import (
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func maintenanceClientModel() *Model {
	m := NewModel()
	m.LocalDeviceID = "D"
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Connected = true
		p.ConnectionEpoch = "epoch"
		p.ApplySnapshot(domain.Snapshot{DeviceID: "D"})
		p.WorkerService = &WorkerService{Build: &domain.WorkerBuild{BuildID: "old", Maintenance: true}, Available: &domain.WorkerBuild{BuildID: "new"}, Phase: WorkerWaiting}
	})
	return m
}
func readyMaintenanceClient(t *testing.T, m *Model) domain.WorkerMaintenanceTarget {
	t.Helper()
	job, err := m.InspectWorkerMaintenance("local", true)
	if err != nil {
		t.Fatal(err)
	}
	target := domain.WorkerMaintenanceTarget{SessionID: "s", Generation: "g", ProcessRef: "p", Revision: 2, QueueRevision: 3}
	b, _ := json.Marshal(domain.WorkerUpdateDiagnosis{DeviceID: "D", BuildID: "old", Blockers: []domain.WorkerUpdateBlocker{{ID: "s", SessionID: "s", Kind: domain.WorkerBlockerEnvironment, Target: &target}}})
	job.Apply(m, b, nil)
	return target
}
func TestMaintenanceUnknownDeliveryOnlyQueriesOriginalOperation(t *testing.T) {
	m := maintenanceClientModel()
	target := readyMaintenanceClient(t, m)
	job, err := m.ApplyWorkerMaintenance("local", []domain.WorkerMaintenanceTarget{target}, false)
	if err != nil {
		t.Fatal(err)
	}
	in := job.Input.(domain.WorkerMaintenanceInput)
	job.Apply(m, nil, errors.New("connection lost"))
	if _, err = m.ApplyWorkerMaintenance("local", []domain.WorkerMaintenanceTarget{target}, false); err == nil {
		t.Fatal("unknown stop was resent")
	}
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	var saved savedState
	if json.Unmarshal(data, &saved) != nil || saved.WorkerMaintenanceIDs["D"] != in.ID {
		t.Fatal("lost query identity")
	}
	query, err := m.QueryWorkerMaintenance("local")
	if err != nil {
		t.Fatal(err)
	}
	if query.Method != "worker.maintenance.get" || query.Input.(protocol.WorkerMaintenanceQuery).ID != in.ID {
		t.Fatal("query did not follow original operation", query)
	}
}
func TestMaintenanceRejectsChangedDeviceEpochAndForceWithoutConsentState(t *testing.T) {
	m := maintenanceClientModel()
	job, err := m.InspectWorkerMaintenance("local", true)
	if err != nil {
		t.Fatal(err)
	}
	m.DeviceStates["local"].ConnectionEpoch = "reconnected"
	b, _ := json.Marshal(domain.WorkerUpdateDiagnosis{DeviceID: "D", BuildID: "old"})
	job.Apply(m, b, nil)
	if m.WorkerMaintenanceClients["D"].Diagnosis != nil {
		t.Fatal("stale diagnosis applied")
	}
	readyMaintenanceClient(t, m)
	if _, err = m.ApplyWorkerMaintenance("local", nil, false); err == nil {
		t.Fatal("empty selection accepted")
	}
	if _, err = m.ApplyWorkerMaintenance("local", []domain.WorkerMaintenanceTarget{{SessionID: "s"}}, true); err == nil {
		t.Fatal("force without awaiting operation")
	}
	m.DeviceStates["local"].WorkerService.Build.Maintenance = false
	if _, err = m.InspectWorkerMaintenance("local", true); err == nil {
		t.Fatal("old worker received unsupported request")
	}
}
func TestMaintenanceReplyCannotCrossDeviceOrOperation(t *testing.T) {
	m := maintenanceClientModel()
	target := readyMaintenanceClient(t, m)
	job, err := m.ApplyWorkerMaintenance("local", []domain.WorkerMaintenanceTarget{target}, false)
	if err != nil {
		t.Fatal(err)
	}
	in := job.Input.(domain.WorkerMaintenanceInput)
	b, _ := json.Marshal(domain.WorkerMaintenance{ID: in.ID, DeviceID: "other", Phase: domain.WorkerMaintenanceCompleted})
	job.Apply(m, b, nil)
	c := m.WorkerMaintenanceClients["D"]
	if c.Operation != nil || !c.QueryOnly {
		t.Fatal("foreign receipt accepted", c)
	}
}

func TestMaintenanceInspectionAfterCancellationAllowsFreshSelection(t *testing.T) {
	m := maintenanceClientModel()
	readyMaintenanceClient(t, m)
	c := m.MaintenanceClient("D")
	c.Operation = &domain.WorkerMaintenance{ID: "cancelled", DeviceID: "D", Phase: domain.WorkerMaintenanceCancelled}
	c.PendingID = "cancelled"
	job, err := m.InspectWorkerMaintenance("local", true)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(domain.WorkerUpdateDiagnosis{DeviceID: "D", BuildID: "old", Operation: c.Operation})
	job.Apply(m, b, nil)
	if c.Operation != nil || c.PendingID != "" {
		t.Fatal("terminal receipt traps the selection page", c)
	}
}

package worker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness/antigravity"
	"shioji/internal/protocol"
)

func TestAntigravityReferencesWithIsolatedCLI(t *testing.T) {
	executable := os.Getenv("SHIOJI_TEST_AGY_WRAPPER")
	if executable == "" {
		t.Skip("requires isolated agy fixture")
	}
	a := antigravity.Adapter{Executable: executable}
	s, w := memoryWorker(t, a)
	ready, err := s.recheckRuntime(a.Descriptor().ID)
	if err != nil || ready.State != domain.RuntimeReady || !ready.Capabilities.Skills || !ready.Capabilities.PathAttachments {
		t.Fatalf("explicit ACP discovery: %+v, %v", ready, err)
	}
	root := w.Folders[0]
	source := filepath.Join(root, "selected.txt")
	if err := os.WriteFile(source, []byte("phase-two-frozen-snapshot-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	q := protocol.GitQuery{WorkspaceID: w.ID}
	snapshot, err := s.snapshotFile(protocol.FileSnapshotQuery{GitQuery: q, Root: root, Path: "selected.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("phase-two-replacement-must-not-appear"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := []byte("phase-two-raw-bytes-must-not-appear\x00")
	binaryRef, err := attachments.New(t.TempDir()).SavePathResource("phase-two-bundle.zip", binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.attachments.Put(w.ID, binaryRef, 0, binary); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(root, ".agents", "skills", "fixture-review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: fixture-review\ndescription: Fixture skill\n---\nphase-two-skill-body-marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.listSkills(protocol.SkillQuery{GitQuery: q, HarnessID: a.Descriptor().ID})
	idx := slices.IndexFunc(catalog.Skills, func(s domain.Skill) bool { return s.Name == "fixture-review" })
	if err != nil || idx < 0 {
		t.Fatalf("skills = %+v, %v", catalog, err)
	}
	ref := catalog.Skills[idx].SkillReference
	var cfg domain.RunConfig
	for _, model := range ready.Models {
		if model.Default {
			cfg = domain.RunConfig{Model: model.ID, Effort: model.DefaultEffort, Permission: "default"}
		}
	}
	op, err := s.submit(domain.Submission{
		OperationID: "reference-input", WorkspaceID: w.ID, HarnessID: a.Descriptor().ID, Config: cfg,
		Text: "Review the provided references.", Attachments: []domain.Attachment{snapshot, binaryRef}, Skills: []domain.SkillReference{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt := awaitReceipt(t, s, op.ID); receipt.State != domain.OperationConfirmed {
		t.Fatalf("reference receipt = %+v", receipt)
	}
	awaitReloadLive(t, func() bool {
		d, err := s.store.Session(op.SessionID, 0, 20)
		if err != nil {
			t.Fatal(err)
		}
		return d.Session.Status == domain.ExecutionCompleted
	})
	d, err := s.store.Session(op.SessionID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.reloadSession(domain.SessionReloadRequest{OperationID: "reference-reload", SessionID: d.Session.ID, Revision: d.Session.Revision}, false)
	if err != nil || reloaded.Session.ThreadID != d.Session.ThreadID || reloaded.Operation.State != "completed" {
		t.Fatalf("cold reload = %+v, %v", reloaded, err)
	}
	after, err := s.store.Session(op.SessionID, 0, 20)
	if err != nil || len(after.Turns) != len(d.Turns) {
		t.Fatalf("reload modified the preserved turns: %v, %+v", err, after.Turns)
	}
	now := time.Now()
	sweepIdle(t, s, now)
	sweepIdle(t, s, now.Add(20*time.Minute))
	if s.currentRuntime(after.Session.ID) != nil {
		t.Fatal("before the compaction the owning worker did not reclaim the idle runtime")
	}
	after, err = s.store.Session(after.Session.ID, 0, 20)
	if err != nil || after.Session.Status != domain.ExecutionCompleted {
		t.Fatalf("idle retirement did not preserve the completed conversation: %+v, %v", after.Session, err)
	}
	cold, ok := s.runtimeReadiness().ForHarness(a.Descriptor().ID)
	if !ok || !cold.Capabilities.CanRequestCompaction() {
		t.Fatal("cold session cannot reach compaction through the worker catalog")
	}
	compaction := domain.ContextActionRequest{OperationID: "agy-compact", SessionID: after.Session.ID, Revision: after.Session.Revision}
	op, err = s.compactContext(compaction)
	if err != nil || op.State != domain.OperationConfirmed {
		t.Fatalf("compact command was not accepted: %+v, %v", op, err)
	}
	awaitReloadLive(t, func() bool {
		result, err := s.store.Session(op.SessionID, 0, 20)
		if err != nil {
			t.Fatal(err)
		}
		return result.Session.ContextAction != nil && result.Session.ContextAction.State == domain.ExecutionCompleted
	})
	after, err = s.store.Session(op.SessionID, 0, 20)
	if err != nil || after.Session.ThreadID != d.Session.ThreadID || after.Session.Status != d.Session.Status {
		t.Fatalf("compaction changed the session identity or lifecycle: %+v, %v", after.Session, err)
	}
	hasEnd := slices.ContainsFunc(after.Items, func(item domain.Item) bool {
		return item.Presentation == "compaction" && item.Final && item.Name == "压缩命令已结束"
	})
	if !hasEnd || after.Session.ContextUsage == nil || after.Session.ContextUsage.TurnID != after.Session.ContextAction.TurnID {
		t.Fatalf("compaction result or usage has stale provenance: %v %+v", hasEnd, after.Session.ContextUsage)
	}
	for _, entry := range after.Items {
		if entry.Kind == "user" && strings.Contains(entry.Text, "Review") && slices.Equal(entry.Skills, []domain.SkillReference{ref}) && slices.Equal(entry.Attachments, []domain.Attachment{snapshot, binaryRef}) {
			return
		}
	}
	t.Fatal("reload did not preserve the typed input and immutable references")
}

func TestAntigravityScheduledQuestionWithIsolatedCLI(t *testing.T) {
	executable := os.Getenv("SHIOJI_TEST_AGY_WRAPPER")
	if executable == "" {
		t.Skip("requires isolated agy fixture")
	}
	a := antigravity.Adapter{Executable: executable}
	s, w := memoryWorker(t, memoryHarness{id: "other"}, a)
	harnessID := a.Descriptor().ID
	device, err := s.recheckRuntime(harnessID)
	ready, found := device.ForHarness(harnessID)
	if err != nil || !found || ready.State != domain.RuntimeReady {
		t.Fatalf("explicit ACP discovery: %+v, %v", ready, err)
	}
	var cfg domain.RunConfig
	for _, model := range ready.Models {
		if model.Default {
			cfg = domain.RunConfig{Model: model.ID, Effort: model.DefaultEffort, Permission: "default"}
		}
	}
	task, err := s.store.SaveSchedule(domain.Schedule{
		ID: "agy-schedule", WorkspaceID: w.ID, HarnessID: harnessID, Config: cfg,
		Name: "Scheduled question", Text: "phase-two-scheduled-question", Enabled: true,
		Rule: domain.ScheduleRule{Kind: "interval", Minutes: 60},
	}, time.Now().Add(-61*time.Minute), scheduleZone())
	if err != nil {
		t.Fatal(err)
	}
	s.wakeSchedules()
	var sessionID string
	var request domain.PendingRequest
	awaitReloadLive(t, func() bool {
		detail, err := s.store.ScheduleDetail(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Runs) == 0 {
			return false
		}
		run := detail.Runs[0]
		if run.State == domain.ScheduleFailed || run.State == domain.ScheduleMissed {
			t.Fatalf("scheduled launch failed: %+v", run)
		}
		if run.SessionID == "" {
			return false
		}
		sessionID = run.SessionID
		if run.Manual {
			t.Fatal("the scheduler did not trigger this run")
		}
		receipt, err := s.store.Operation(domain.ScheduleOperationID(run.ID))
		if err != nil || receipt.SessionID != sessionID {
			t.Fatalf("scheduled input lost ordinary admission: %+v, %v", receipt, err)
		}
		session, err := s.store.Session(sessionID, 0, 20)
		if err != nil || session.Session.HarnessID != harnessID {
			t.Fatalf("schedule selected the wrong harness: %+v, %v", session.Session, err)
		}
		if run.State == domain.ScheduleCompleted {
			t.Fatalf("scheduled run completed without the native form: requests=%+v items=%+v", session.Requests, session.Items)
		}
		for _, candidate := range session.Requests {
			if candidate.Kind == "form" && candidate.State == domain.RequestPending {
				request = candidate
				return true
			}
		}
		return false
	})
	if request.TurnID == "" || request.ItemID == "" || len(request.Fields) != 4 {
		t.Fatalf("question lost native tool and form associations: %+v", request)
	}
	if err := s.respondRequest(AnswerInput{SessionID: sessionID, Answer: domain.Answer{
		RequestID: request.ID, Revision: request.Revision, Decision: "accept",
		Answers: map[string][]string{"q1": {"2"}, "q2": {"1"}, "q2_other": {"Lint"}},
	}}); err != nil {
		t.Fatal(err)
	}
	awaitReloadLive(t, func() bool {
		detail, err := s.store.ScheduleDetail(task.ID)
		return err == nil && len(detail.Runs) == 1 && detail.Runs[0].State == domain.ScheduleCompleted
	})
}

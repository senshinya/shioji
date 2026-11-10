package ui

import (
	"bytes"
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func compactView(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.WorkerMinor = 30
	m.Readiness.Capabilities.Compact = true
	m.Snapshot.Sessions[0].ThreadID = "source"
	m.Snapshot.Sessions[0].Revision = 10
	m.Snapshot.Sessions[0].Status = "completed"
	m.Snapshot.Sessions[0].ContextUsage = contextReport(332500, 475000)
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	return v, ui
}
func contextJobs(v *View) []app.Job {
	var out []app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "session.context.compact" || job.Method == "session.context.operation" || job.Method == "session.submit" || job.Method == "queue.add" {
			out = append(out, job)
		}
	}
	return out
}
func TestNativeContextActionPreservesOrdinaryDraftAndGuardsDoubleClick(t *testing.T) {
	v, ui := compactView(t)
	key := v.Model.DraftKey()
	v.Model.Drafts[key] = "keep ordinary draft"
	ui.Frame()
	hoverContextMeter(t, ui)
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("压缩上下文"); err != nil {
		t.Fatal("compact action missing", err)
	}
	ui.Frame()
	ui.Click("压缩上下文")
	jobs := contextJobs(v)
	if len(jobs) != 1 || jobs[0].Method != "session.context.compact" {
		t.Fatal("duplicate or incorrect command", jobs)
	}
	if v.Model.Drafts[key] != "keep ordinary draft" {
		t.Fatal("button consumed draft")
	}
	in := jobs[0].Input.(domain.ContextActionRequest)
	b, _ := json.Marshal(domain.ContextActionResult{Operation: domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "compact", State: "confirmed"}, Session: v.contextSession()})
	jobs[0].Apply(v.Model, b, nil)
	if v.Model.Drafts[key] != "keep ordinary draft" {
		t.Fatal("receipt consumed draft")
	}
}
func TestNativeCompactCommandDoesNotSubmitOrQueueAndKeepsLaterDraft(t *testing.T) {
	v, ui := compactView(t)
	ui.Click("消息输入")
	ui.Type("/compact")
	ui.Advance(200 * time.Millisecond)
	ui.Key(0, n.KeyEnter)
	jobs := contextJobs(v)
	if len(jobs) != 1 || jobs[0].Method != "session.context.compact" {
		t.Fatal("slash command became a user message or was swallowed", jobs, ui.Texts())
	}
	in := jobs[0].Input.(domain.ContextActionRequest)
	v.Model.Drafts[v.Model.DraftKey()] = "new draft"
	b, _ := json.Marshal(domain.ContextActionResult{Operation: domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "compact", State: "confirmed"}, Session: v.contextSession()})
	jobs[0].Apply(v.Model, b, nil)
	if v.Model.Drafts[v.Model.DraftKey()] != "new draft" {
		t.Fatal("receipt erased later input")
	}
}
func TestCompactCommandRejectsParametersAndHomeWithoutSending(t *testing.T) {
	for _, home := range []bool{false, true} {
		v, ui := compactView(t)
		if home {
			v.Model.SessionID = ""
			ui.Frame()
		}
		key := v.Model.DraftKey()
		text := "/compact additional text"
		if home {
			text = "/compact"
		}
		v.Model.Drafts[key] = text
		v.submit()
		ui.Frame()
		ui.Advance(300 * time.Millisecond)
		if len(contextJobs(v)) != 0 || v.Model.Drafts[key] != text {
			t.Fatal("invalid command sent or consumed", home)
		}
		if _, ok := ui.Find("压缩命令提示"); !ok {
			t.Fatal("invalid command has no inline feedback", home, ui.Texts())
		}
	}
}

func TestNativeCompactMenuStaysOpenAndTabCompletesWithoutExecuting(t *testing.T) {
	v, ui := compactView(t)
	ui.Click("消息输入")
	ui.Type("/")
	ui.Advance(200 * time.Millisecond)
	state := v.fileMention
	if state == nil {
		t.Fatal("no slash menu")
	}
	ui.Type("co")
	ui.Advance(200 * time.Millisecond)
	if v.fileMention != state || !ui.Focused("消息输入") {
		t.Fatal("filter recreated menu or stole focus")
	}
	ui.Key(0, n.KeyTab)
	ui.Advance(200 * time.Millisecond)
	if v.Model.Drafts[v.Model.DraftKey()] != "/compact" || len(contextJobs(v)) != 0 {
		t.Fatal("Tab executed instead of completing command")
	}
	ui.Key(0, n.KeyEnter)
	if jobs := contextJobs(v); len(jobs) != 1 || jobs[0].Method != "session.context.compact" {
		t.Fatal(jobs)
	}
}

func TestCompactMenuDoesNotInterpretCommandInsideOrdinaryText(t *testing.T) {
	v, ui := compactView(t)
	ui.Click("消息输入")
	ui.Type("explain /co")
	ui.Advance(200 * time.Millisecond)
	if v.fileMention != nil && v.fileMention.compact {
		t.Fatal("inline literal offered a destructive maintenance command")
	}
}

func TestTypingDuringCompressionQueuesInsteadOfSteeringMaintenance(t *testing.T) {
	v, _ := compactView(t)
	m := v.Model
	m.Readiness.Capabilities.Steer = true
	m.Snapshot.Sessions[0].Status = "running"
	m.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "comp", SourceRef: "source", TurnID: "ct", State: "running"}
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	d.Turns = []domain.Turn{{ID: "ct", Presentation: "compaction", Status: "running"}}
	m.Details["s"] = d
	m.Drafts[m.DraftKey()] = "next task"
	v.sendMessage(true)
	queued := false
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		queued = queued || job.Method == "turn.enqueue"
		if job.Method == "turn.steer" || job.Method == "session.submit" {
			t.Fatal("ordinary input changed maintenance turn", job.Method)
		}
	}
	if !queued {
		t.Fatal("ordinary message was not queued", m.Error)
	}
}
func TestManualCompactionHasNoOrdinaryReplyFooter(t *testing.T) {
	rows := []transcriptRow{{key: "compact", item: domain.Item{ID: "compact", TurnID: "ct", Kind: "activity", Presentation: "compaction"}, end: 0}}
	got := withTurnFooters(rows, []domain.Item{rows[0].item}, map[string]domain.Turn{"ct": {ID: "ct", Presentation: "compaction", Status: "completed"}})
	if len(got) != 1 {
		t.Fatal("maintenance fabricated ordinary reply footer", got)
	}
}

func TestCompactionHandoffRetainsRowRingAndComposerGeometry(t *testing.T) {
	if dir := os.Getenv("SHIOJI_COMPACT_CAPTURE"); dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	}
	v, ui := compactView(t)
	hoverContextMeter(t, ui)
	ui.Advance(300 * time.Millisecond)
	ui.Click("压缩上下文")
	jobs := contextJobs(v)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	in := jobs[0].Input.(domain.ContextActionRequest)
	ui.Advance(300 * time.Millisecond)
	before, _ := ui.Find("正在压缩上下文")
	composer, _ := ui.Find("消息编辑器")
	ring, _ := ui.Find("上下文圆环")
	pixels := contextPixels(ui, ring)
	saveNativeSkillImage(t, ui, "handoff-before.png")
	v.Model.Snapshot.Sessions[0].Status = "running"
	v.Model.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: in.OperationID, RequestRevision: in.Revision, SourceRef: "source", TurnID: "ct", State: "running"}
	d := v.Model.Details["s"]
	d.Session = v.Model.Snapshot.Sessions[0]
	d.Turns = []domain.Turn{{ID: "ct", Presentation: "compaction", Status: "running"}}
	d.Items = append(d.Items, domain.Item{ID: "actual-compact", TurnID: "ct", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	after, _ := ui.Find("正在压缩上下文")
	endComposer, _ := ui.Find("消息编辑器")
	endRing, _ := ui.Find("上下文圆环")
	adopted := contextPixels(ui, endRing)
	saveNativeSkillImage(t, ui, "handoff-after.png")
	if before != after || composer != endComposer || ring != endRing || !bytes.Equal(pixels, adopted) {
		t.Fatal("source handoff moved/reset native elements", before, after, composer, endComposer, ring, endRing)
	}
}

func TestCompactionUnknownRestoresAQueryWithoutClearingDraft(t *testing.T) {
	v, ui := compactView(t)
	m := v.Model
	m.Drafts[m.DraftKey()] = "keep"
	m.Snapshot.Sessions[0].Status = "unknown"
	m.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "unknown", RequestRevision: 8, SourceRef: "source", State: "unknown"}
	ui.Frame()
	hoverContextMeter(t, ui)
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("压缩上下文"); err != nil {
		t.Fatal(err)
	}
	jobs := contextJobs(v)
	if len(jobs) != 1 || jobs[0].Method != "session.context.operation" {
		t.Fatal("reconnect issued a second compression", jobs)
	}
	if m.Drafts[m.DraftKey()] != "keep" {
		t.Fatal("query cleared normal draft")
	}
}

func TestVerifiedCompactionReplyPrecedesAndSurvivesOlderJournalMetadata(t *testing.T) {
	v, ui := compactView(t)
	m := v.Model
	m.Snapshot.Sessions[0].Status = "unknown"
	m.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "unknown", RequestRevision: 8, SourceRef: "source", State: "unknown"}
	ui.Frame()
	hoverContextMeter(t, ui)
	ui.Advance(300 * time.Millisecond)
	ui.Click("压缩上下文")
	jobs := contextJobs(v)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	jobs[0].Apply(m, json.RawMessage(`{"operation":{"id":"unknown","sessionId":"s","kind":"compact","state":"confirmed"},"session":{"id":"s","workspaceId":"w","threadId":"source","revision":12,"status":"completed","contextAction":{"operationId":"unknown","requestRevision":8,"sourceRef":"source","turnId":"ct","state":"completed","previousStatus":"completed"}}}`), nil)
	ui.Frame()
	if v.contextSession().Status != "completed" || v.contextAction().State != "completed" {
		t.Fatal("verified result flickered back to unknown before journal", v.contextSession())
	}
	stale := m.Snapshot.Sessions[0]
	stale.Revision = 11
	stale.Status = "unknown"
	stale.ContextAction = &domain.ContextAction{OperationID: "unknown", SourceRef: "source", State: "unknown"}
	m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "s", Type: "session", Session: &stale})
	ui.Frame()
	if v.contextAction().State != "completed" {
		t.Fatal("older journal metadata restored unknown state")
	}
	if m.Details["s"].Session.Status != "completed" {
		t.Fatal("older journal metadata replaced authoritative detail")
	}
}

func TestCompactionResultProjectsAcrossOlderItemDelivery(t *testing.T) {
	v, _ := compactView(t)
	for _, state := range []domain.ExecutionState{"completed", "interrupted", "failed", "unknown"} {
		v.Model.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "comp", SourceRef: "source", TurnID: "ct", State: state}
		rows := v.contextRows([]transcriptRow{{key: "item", item: domain.Item{ID: "item", Kind: "activity", Presentation: "compaction", TurnID: "ct", Status: "running", Name: "正在压缩上下文"}}})
		if len(rows) != 1 || !rows[0].item.Final || rows[0].item.Status != string(state) || rows[0].item.Name == "正在压缩上下文" {
			t.Fatal("stale item restored running state", state, rows)
		}
	}
}

func TestContextCompactionNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_COMPACT_CAPTURE")
	if dir == "" {
		t.Skip("set SHIOJI_COMPACT_CAPTURE")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for _, dark := range []bool{false, true} {
		v, ui := compactView(t)
		m := v.Model
		m.Snapshot.Workspaces[0].Name = "红黑树"
		m.Snapshot.Sessions[0].Title = "红黑树旋转讲解"
		m.Drafts[m.DraftKey()] = "继续补齐删除节点的边界条件。"
		m.Readiness.Models = []domain.Model{{ID: "gpt-6.1-sol", Default: true, Efforts: []string{"medium"}}}
		m.Configs[m.DraftKey()] = domain.RunConfig{Model: "gpt-6.1-sol", Effort: "medium", Permission: "read-only"}
		d := m.Details["s"]
		d.Items = []domain.Item{{ID: "user", TurnID: "root", Kind: "user", Text: "用 Go 讲清楚红黑树左旋，保留节点的父子关系。", Final: true}, {ID: "reply", TurnID: "root", Kind: "assistant", Text: "左旋会把当前节点的右孩子提上来，当前节点成为它的左孩子。中间子树接回当前节点右侧，大小顺序保持不变。\n\n```go\nfunc rotateLeft(x *Node) *Node {\n    y := x.Right\n    x.Right = y.Left\n    if y.Left != nil {\n        y.Left.Parent = x\n    }\n    y.Parent = x.Parent\n    y.Left = x\n    x.Parent = y\n    return y\n}\n```\n\n先接好中间子树，再处理父节点，最后把原节点挂到新根左边。", Final: true}}
		d.Turns = []domain.Turn{{ID: "root", Status: "completed"}}
		m.Details["s"] = d
		name := "light"
		if dark {
			name = "dark"
		}
		ui.SetDark(dark)
		ui.Frame()
		ui.Advance(time.Second)
		hoverContextMeter(t, ui)
		ui.Advance(400 * time.Millisecond)
		saveNativeSkillImage(t, ui, name+"-context.png")
		ui.Move(0, 0)
		ui.Advance(400 * time.Millisecond)
		ui.Click("消息输入")
		m.Drafts[m.DraftKey()] = "/co"
		ui.Frame()
		ui.Key(0, n.KeyEnd)
		ui.Advance(250 * time.Millisecond)
		if v.fileMention != nil {
			v.fileMention.skills = []domain.Skill{{SkillReference: domain.SkillReference{ID: "review", Name: "code-review"}, Description: "检查代码变更"}, {SkillReference: domain.SkillReference{ID: "commit", Name: "commit"}, Description: "整理并提交修改"}}
			ui.Frame()
			ui.Advance(250 * time.Millisecond)
		}
		saveNativeSkillImage(t, ui, name+"-slash.png")
		m.Drafts[m.DraftKey()] = "继续补齐删除节点的边界条件。"
		m.Snapshot.Sessions[0].Status = "running"
		m.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "native", RequestRevision: 10, SourceRef: "source", TurnID: "ct", State: "running", PreviousStatus: "completed"}
		d.Session = m.Snapshot.Sessions[0]
		now := time.Now()
		d.Items = append(d.Items, domain.Item{ID: "context-command:native", TurnID: "ct", Kind: "command", Presentation: "compact", Name: "压缩上下文", Status: "accepted", Final: true, SentAt: &now}, domain.Item{ID: "compact", TurnID: "ct", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"})
		d.Turns = append(d.Turns, domain.Turn{ID: "ct", Presentation: "compaction", Status: "running"})
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(3 * time.Second)
		saveNativeSkillImage(t, ui, name+"-running.png")
		m.Snapshot.Sessions[0].Status = "completed"
		m.Snapshot.Sessions[0].ContextAction.State = "completed"
		m.Snapshot.Sessions[0].ContextUsage = contextReport(90250, 475000)
		d.Session = m.Snapshot.Sessions[0]
		d.Items[len(d.Items)-1].Name = "上下文已压缩"
		d.Items[len(d.Items)-1].Status = "completed"
		d.Items[len(d.Items)-1].Final = true
		d.Turns[len(d.Turns)-1].Status = "completed"
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, name+"-complete.png")
	}
}

func TestSecondCompactCommandSurvivesOldCompactionReceipt(t *testing.T) {
	v, ui := compactView(t)
	m := v.Model
	m.Drafts[m.DraftKey()] = "/compact"
	v.beginContextCompaction(true)
	jobs := contextJobs(v)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	in := jobs[0].Input.(domain.ContextActionRequest)
	m.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: in.OperationID, SourceRef: "source", TurnID: "compact-turn", State: "completed"}
	v.syncContextInput()
	if m.Drafts[m.DraftKey()] != "" {
		t.Fatal("first command was not consumed")
	}
	m.Drafts[m.DraftKey()] = "/compact"
	ui.Frame()
	if dir := os.Getenv("SHIOJI_COMPACT_CAPTURE"); dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
		saveNativeSkillImage(t, ui, "repeat-command.png")
	}
	if m.Drafts[m.DraftKey()] != "/compact" {
		t.Fatal("old completed operation erased second command before it was sent")
	}
}

func TestLateCompactionAcceptanceKeepsRetypedIdenticalCommand(t *testing.T) {
	for _, path := range []string{"receipt", "source"} {
		t.Run(path, func(t *testing.T) {
			v, ui := compactView(t)
			m := v.Model
			ui.Click("消息输入")
			ui.Type("/compact")
			ui.Key(0, n.KeyEnter)
			jobs := contextJobs(v)
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			in := jobs[0].Input.(domain.ContextActionRequest)
			ui.Key(n.Cmd, n.KeyA)
			ui.Type("/compact")
			if m.Drafts[m.DraftKey()] != "/compact" {
				t.Fatal("could not retype command")
			}
			session := v.contextSession()
			session.Revision++
			session.ContextAction = &domain.ContextAction{OperationID: in.OperationID, SourceRef: "source", TurnID: "ct", State: "completed"}
			if path == "receipt" {
				body, _ := json.Marshal(domain.ContextActionResult{Operation: domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "compact", State: "confirmed"}, Session: session})
				jobs[0].Apply(m, body, nil)
			} else {
				m.Snapshot.Sessions[0] = session
				v.syncContextInput()
			}
			ui.Frame()
			if m.Drafts[m.DraftKey()] != "/compact" {
				t.Fatal("late acceptance erased a newer identical draft")
			}
		})
	}
}

func TestCompactionRecordUsesReadonlyRightAlignedTag(t *testing.T) {
	v, ui := compactView(t)
	m := v.Model
	item := domain.Item{ID: "context-command:record", Kind: "command", Presentation: "compact", Name: "压缩上下文", Status: "accepted", Final: true}
	d := m.Details["s"]
	d.Items = append(d.Items, item)
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	tag, ok := ui.Find("操作：压缩上下文")
	if !ok || tag.X < 700 {
		t.Fatal("command is not a right-aligned tag", tag, ui.Texts())
	}
	if _, ok := ui.Find("修改并重新开始"); ok {
		t.Fatal("command has an edit action")
	}
	if err := m.BeginHistoryEdit(item); err == nil {
		t.Fatal("command opened history editor")
	}
	if v.messageCopyText(item) != "压缩上下文" {
		t.Fatal("copy lost the tag label")
	}
	if err := ui.Click("复制消息"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "压缩上下文" {
		t.Fatal("native copy lost the command name", ui.Clipboard())
	}
}

func TestCompactMenuUsesNamedActionAlongsideSkills(t *testing.T) {
	v, ui := compactView(t)
	ui.Click("消息输入")
	ui.Type("/co")
	ui.Advance(250 * time.Millisecond)
	if _, ok := ui.Find("压缩上下文命令"); !ok {
		t.Fatal("compact action is absent", ui.Texts())
	}
	row, _ := ui.Find("压缩上下文命令")
	name, ok := ui.Find("压缩上下文")
	if !ok || name.X >= row.X+row.W/2 {
		t.Fatal("semantic action name is not in the primary label column", name, row)
	}
	_ = v
}

func TestCompactionRequiresWorkerThatPersistsCommandRecords(t *testing.T) {
	v, _ := compactView(t)
	m := v.Model
	m.WorkerMinor = 29
	m.Drafts[m.DraftKey()] = "/compact"
	v.submit()
	if len(contextJobs(v)) != 0 || m.Drafts[m.DraftKey()] != "/compact" {
		t.Fatal("older worker accepted an action it cannot persist as a command record")
	}
}

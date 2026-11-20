package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestArchiveMenuOpensFullListAndDirectHistoryPreservesDraft(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 40
	m.Drafts["home"] = "keep home"
	m.Drafts["session:s"] = "keep ordinary draft"
	m.Snapshot.Sessions[0].Archived = true
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	if err := ui.Click("工作区菜单"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("已归档…"); err != nil {
		t.Fatal("archive list entry missing", err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("归档工作区列") || !ui.HasText("1 个会话") {
		t.Fatal("full archive list not shown", ui.Texts())
	}
	if err := ui.Click("查看归档会话：Layout"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("Aligned reply") {
		t.Fatal("archive did not open existing history")
	}
	if err := ui.Click("返回已归档"); err != nil {
		t.Fatal(err)
	}
	if !ui.HasText("1 个会话") || m.Drafts["home"] != "keep home" || m.Drafts["session:s"] != "keep ordinary draft" {
		t.Fatal("archive navigation lost draft or return state")
	}
}

func archiveViewFixture() (*View, *n.Tester) {
	m := layoutModel()
	m.WorkerMinor = 40
	m.Snapshot.Sessions[0].Archived = true
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	v.openArchive()
	return v, n.NewTester(v.View, 1240, 820)
}

func TestArchiveLastRowRemainsReachableAfterResize(t *testing.T) {
	v, ui := archiveViewFixture()
	for i := 2; i <= 14; i++ {
		v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{
			ID: fmt.Sprintf("archive-%d", i), WorkspaceID: "w", Title: fmt.Sprintf("归档会话 %02d", i), Archived: true,
		})
	}
	ui.SetDark(true)
	for _, size := range []struct{ width, height int }{{1240, 806}, {1240, 760}, {850, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			ui.SetSize(size.width, size.height)
			ui.Advance(time.Second)
			heading, ok := ui.Find("已归档")
			if !ok || heading.Y != 8 {
				t.Fatal("archive heading does not align with the sidebar frame", heading)
			}
			list, ok := ui.Find("已归档会话列表")
			if !ok {
				t.Fatal("archive list is missing")
			}
			ui.Scroll(list.X+100, list.Y+40, 0, -2000)
			ui.Scroll(list.X+100, list.Y+40, 0, 2000)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("archive-bottom-%d.png", size.height))
			row, ok := ui.Find("查看归档会话：归档会话 14")
			if !ok || row.H < 39 || row.Y+row.H > float32(size.height) {
				t.Fatalf("last row remains clipped at scroll end: row=%+v list=%+v", row, list)
			}
		})
	}
}

func TestArchiveDeleteConfirmationTargetsStableObjectAndRestoresFailure(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Drafts["home"] = "keep home"
	r, ok := ui.Find("查看归档会话：Layout")
	if !ok {
		t.Fatal("missing archive row")
	}
	ui.Move(r.X+20, r.Y+r.H/2)
	ui.Frame()
	if err := ui.Click("彻底删除会话：Layout"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("项目文件和 Git 仓库不会删除") {
		t.Fatal("deletion scope missing")
	}
	ui.Click("取消")
	ui.Advance(time.Second)
	if len(v.Model.Snapshot.Sessions) != 1 {
		t.Fatal("cancel deleted session")
	}
	ui.Move(r.X+20, r.Y+r.H/2)
	ui.Frame()
	ui.Click("彻底删除会话：Layout")
	ui.Advance(time.Second)
	ui.Click("彻底删除")
	var job app.Job
	for len(v.Controller.Jobs) > 0 {
		candidate := <-v.Controller.Jobs
		if candidate.Method == "session.delete" {
			job = candidate
			break
		}
	}
	if job.Method == "" || job.ConnectionID != "local" {
		t.Fatal("delete routed incorrectly", job)
	}
	in := job.Input.(protocol.ArchiveDeleteInput)
	if in.SessionID != "s" || in.Revision != v.Model.Snapshot.Sessions[0].Revision {
		t.Fatal("delete did not capture revision", in)
	}
	job.Apply(v.Model, nil, errors.New("revision changed"))
	ui.Frame()
	if !ui.HasText("revision changed") {
		t.Fatal("failed deletion hid confirmation error")
	}
	if err := ui.Click("重试失败项"); err != nil {
		t.Fatal(err)
	}
	for len(v.Controller.Jobs) > 0 {
		candidate := <-v.Controller.Jobs
		if candidate.Method == "session.delete" {
			job = candidate
			break
		}
	}
	body, _ := json.Marshal(domain.ArchiveDeletion{DeviceID: v.Model.Snapshot.DeviceID, SessionIDs: []string{"s"}})
	job.Apply(v.Model, body, nil)
	if len(v.Model.Snapshot.Sessions) != 0 {
		t.Fatalf("receipt did not remove session: job=%s body=%s dialog=%+v", job.Method, body, v.archive.deletion)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if len(v.Model.Snapshot.Sessions) != 0 || !ui.HasText("没有已归档的会话") || v.Model.Drafts["home"] != "keep home" {
		t.Fatal("successful delete did not update list independently", ui.Texts())
	}
}

func TestArchivedWorkspaceDrilldownAndSearchKeepAllRows(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "old", Name: "旧版项目", Archived: true})
	for i := 0; i < 75; i++ {
		v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: fmt.Sprint("old-", i), WorkspaceID: "old", Title: fmt.Sprint("历史会话 ", i), Status: "completed"})
	}
	ui.Frame()
	if !ui.HasText("76 个会话") {
		t.Fatal("global archive truncated", ui.Texts())
	}
	ui.Click("搜索标题或工作区")
	ui.Type("旧版项目")
	ui.Frame()
	if !ui.HasText("75 个会话") {
		t.Fatal("workspace search missed inherited archives")
	}
	ui.Click("会话")
	ui.Key(0, n.KeyRight)
	ui.Advance(time.Second)
	if err := ui.Click("查看归档工作区：旧版项目"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("75 个会话") || !ui.HasText("工作区已归档") {
		t.Fatal("workspace drilldown not shown", ui.Texts())
	}
	ui.Click("返回已归档工作区")
	ui.Advance(time.Second)
	if !ui.HasText("1 个工作区") {
		t.Fatal("workspace return lost tab")
	}
}

func TestArchiveSettingUsesFourChoicesAndPersistsNever(t *testing.T) {
	v, _ := archiveViewFixture()
	m := v.Model
	m.Snapshot.DeviceID = "local-mac"
	m.Snapshot.ArchivePolicy = &domain.ArchivePolicy{DeviceID: "local-mac", Days: 30, Revision: 1}
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	if err := ui.Click("设置归档保留期 · 这台 Mac"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	for _, label := range []string{"7 天", "14 天", "30 天", "永久保留"} {
		if !ui.HasText(label) {
			t.Fatal("missing retention choice", label, ui.Texts())
		}
	}
	if err := ui.Click("永久保留"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if change, ok := m.ArchivePolicyChanges["local-mac"]; !ok || change.Days != 0 || !ui.HasText("待同步为永久保留") {
		t.Fatal("pending selection hid the current policy", ui.Texts())
	}
	policy := &domain.ArchivePolicy{DeviceID: "local-mac", Days: 0, Revision: 2}
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "archive.policy", ArchivePolicy: policy}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("永久保留") || ui.HasText("待同步") {
		t.Fatal("device did not reflect the confirmed choice", ui.Texts())
	}
}

func TestArchiveRetentionChangesOnlySelectedDevice(t *testing.T) {
	for _, size := range []struct {
		width, height int
		dark          bool
	}{{968, 756, true}, {760, 600, false}} {
		t.Run(fmt.Sprint(size.width), func(t *testing.T) {
			v, main := archiveViewFixture()
			m := v.Model
			m.LocalDeviceID = "local-mac"
			m.LocalDeviceName = "MBA at Home"
			m.Connections = []app.DeviceConnection{
				{ID: "work", DeviceID: "work-mac", Name: "MBP at Work"},
				{ID: "mini", DeviceID: "mini-mac", Name: "MacMini at Home"},
			}
			m.DeviceStates = map[string]*app.DeviceState{}
			for key, id := range map[string]string{"local": "local-mac", "work": "work-mac", "mini": "mini-mac"} {
				m.DeviceStates[key] = &app.DeviceState{Connected: true, WorkerMinor: 40, ConnectionEpoch: key, Snapshot: domain.Snapshot{
					DeviceID: id, ArchivePolicy: &domain.ArchivePolicy{DeviceID: id, Days: 7, Revision: 2},
				}}
			}
			s := NewSettings(v)
			ui := n.NewTester(s.View, size.width, size.height)
			ui.SetDark(size.dark)
			v.OpenSettings = func() {}
			main.Frame()
			main.Advance(time.Second)
			if err := main.Click("归档保留设置"); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("archive-retention-%d.png", size.width))
			if len(m.ArchivePolicyChanges) != 0 {
				t.Fatal("opening settings changed archive preferences")
			}
			if err := ui.Click("设置归档保留期 · MBP at Work"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click("14 天"); err != nil {
				t.Fatal(err)
			}
			if change := m.ArchivePolicyChanges["work-mac"]; len(m.ArchivePolicyChanges) != 1 || change.Days != 14 || change.Revision != 2 {
				t.Fatal("choice was not scoped to MBP at Work", m.ArchivePolicyChanges)
			}
			jobs := m.ArchivePolicyJobs(nil)
			if len(jobs) != 1 || jobs[0].ConnectionID != "work" {
				t.Fatal("selection did not produce a single device job", jobs)
			}
			body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: "work-mac", Days: 14, Revision: 3})
			m.ApplyDeviceUpdate("work", func(p *app.Model) { jobs[0].Apply(p, body, nil) })
			ui.Frame()
			ui.Advance(time.Second)
			if size.width == 760 {
				saveNativeSkillImage(t, ui, "archive-retention-mixed-760.png")
			}
			if !ui.HasText("14 天") || !ui.HasText("7 天") || ui.HasText("待同步") {
				t.Fatalf("mixed policies lack device-specific values: %v", ui.Texts())
			}
			m.DeviceStates["mini"].Connected = false
			if err := ui.Click("设置归档保留期 · MacMini at Home"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click("30 天"); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("上次确认 7 天") || !ui.HasText("重连后同步为 30 天") || len(m.ArchivePolicyChanges) != 1 || m.ArchivePolicyChanges["mini-mac"].Days != 30 {
				t.Fatalf("pending offline change hid the last known policy: %v", ui.Texts())
			}
		})
	}
}

func TestArchiveColumnsShareStableNativeBaselines(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{820, 1240} {
			t.Run(fmt.Sprintf("dark=%v/width=%d", dark, width), func(t *testing.T) {
				v, _ := archiveViewFixture()
				v.Model.Snapshot.Sessions = nil
				for i, title := range []string{"check gyp", "design", "中文工作区", "English 与中文"} {
					v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: fmt.Sprint(i), WorkspaceID: "w", Title: title, Archived: true, Status: "completed"})
				}
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				header, _ := ui.Find("归档工作区列")
				var firstX float32
				for i, title := range []string{"check gyp", "design", "中文工作区", "English 与中文"} {
					name, ok := ui.Find(title)
					if !ok {
						t.Fatal("name not rendered", title)
					}
					metadata, ok := ui.Find("归档归属：" + title)
					if !ok {
						t.Fatal("metadata missing")
					}
					if i == 0 {
						firstX = name.X
					}
					if math.Abs(float64(name.X-firstX)) > 1 || math.Abs(float64(metadata.X-header.X)) > 1 {
						t.Fatal("archive columns drifted", title, name, metadata, header)
					}
					baseline := func(r n.Rect, size float32, label string) float32 {
						line := n.ShapeText(label, n.Font{Family: "system-ui", Size: size}, 0).Lines()[0]
						return r.Y + (20-line.Ascent-line.Descent)/2 + line.Ascent
					}
					if delta := baseline(name, 13, title) - baseline(metadata, 12, "Example"); math.Abs(float64(delta)) > 1 {
						t.Fatal("text columns do not share baseline", title, delta, name, metadata)
					}
				}
			})
		}
	}
}

func TestArchivePageLeavesForWorkspaceSettings(t *testing.T) {
	_, ui := archiveViewFixture()
	if err := ui.RightClick("Example"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("工作区设置"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("1 个会话") || !ui.HasText("工作区名称") {
		t.Fatal("workspace settings remained behind archive page", ui.Texts())
	}
}

func TestArchiveListDoesNotRequestHiddenCheckoutState(t *testing.T) {
	v, _ := archiveViewFixture()
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "git.status" {
			t.Fatal("archive browsing requested the hidden conversation's checkout")
		}
	}
}

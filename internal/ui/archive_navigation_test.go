package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func archiveNavigationFixture() (*View, *n.Tester) {
	m := layoutModel()
	m.WorkerMinor = 44
	m.Drafts["home"] = "首页草稿"
	m.Drafts["session:s"] = "原会话草稿"
	archived := domain.Session{ID: "archived", WorkspaceID: "w", Title: "归档会话", Status: "completed", Archived: true}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, archived)
	m.Details[archived.ID] = domain.SessionDetail{Session: archived, Items: []domain.Item{{ID: "archived-answer", Kind: "assistant", Text: "归档历史正文", Final: true}}}
	v := New(m, app.NewController(""), "")
	return v, n.NewTester(v.View, 1240, 820)
}

func enterArchiveFromMenu(t *testing.T, ui *n.Tester) {
	t.Helper()
	if err := ui.Click("工作区菜单"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("已归档…"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
}

func TestArchiveReturnsToOriginalSessionAndDraft(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := archiveNavigationFixture()
			ui.SetDark(dark)
			enterArchiveFromMenu(t, ui)
			saveNativeSkillImage(t, ui, fmt.Sprintf("archive-navigation-dark-%v.png", dark))
			if err := ui.Click("返回会话"); err != nil {
				t.Fatal("archive list has no direct return to its source conversation", err)
			}
			ui.Advance(time.Second)
			if v.archive.open || v.Model.SessionID != "s" || !ui.HasText("Aligned reply") || v.Model.Drafts["session:s"] != "原会话草稿" || v.Model.Drafts["home"] != "首页草稿" {
				t.Fatal("archive return lost its source conversation or drafts")
			}
		})
	}
}

func TestArchiveReturnKeepsOriginalSourceAcrossHistoryVisits(t *testing.T) {
	v, ui := archiveNavigationFixture()
	enterArchiveFromMenu(t, ui)
	for range 2 {
		if err := ui.Click("查看归档会话：归档会话"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if !ui.HasText("归档历史正文") {
			t.Fatal("archive history not shown")
		}
		if err := ui.Click("返回已归档"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
	}
	if err := ui.Click("返回会话"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.Model.SessionID != "s" || v.archive.open || v.archive.viewing != "" || !ui.HasText("Aligned reply") || v.Model.Drafts["session:s"] != "原会话草稿" {
		t.Fatal("archive history replaced the original return destination")
	}
}

func TestArchiveReturnRestoresParallelFocusAndHome(t *testing.T) {
	for _, source := range []string{"parallel", "home"} {
		t.Run(source, func(t *testing.T) {
			v, _ := archiveNavigationFixture()
			m := v.Model
			label := "返回会话"
			if source == "home" {
				m.SessionID = ""
				label = "返回新建会话"
			} else {
				other := domain.Session{ID: "secondary", WorkspaceID: "w", Title: "并排会话", Status: "completed"}
				m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
				m.Details[other.ID] = domain.SessionDetail{Session: other, Items: []domain.Item{{ID: "secondary-answer", Kind: "assistant", Text: "并排正文", Final: true}}}
				m.SecondarySessionID, m.SecondaryWorkspaceID, m.FocusSecondary = "secondary", "w", true
				m.Drafts["session:secondary"] = "并排草稿"
			}
			ui := n.NewTester(v.View, 1440, 820)
			ui.Advance(time.Second)
			enterArchiveFromMenu(t, ui)
			if err := ui.Click("查看归档会话：归档会话"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click("返回已归档"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click(label); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if source == "parallel" && (m.SessionID != "s" || m.SecondarySessionID != "secondary" || !m.FocusSecondary || m.Drafts["session:secondary"] != "并排草稿") {
				t.Fatal("archive return lost parallel selection, focus or draft")
			}
			if source == "home" && (m.SessionID != "" || m.Drafts["home"] != "首页草稿") {
				t.Fatal("archive return lost the home composer or its draft")
			}
		})
	}
}

func TestArchiveReturnDoesNotRestoreDeletedSource(t *testing.T) {
	for _, workspaceDeleted := range []bool{false, true} {
		t.Run(fmt.Sprint(workspaceDeleted), func(t *testing.T) {
			v, ui := archiveNavigationFixture()
			enterArchiveFromMenu(t, ui)
			if err := ui.Click("查看归档会话：归档会话"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click("返回已归档"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			deletion := domain.ArchiveDeletion{SessionIDs: []string{"s"}}
			if workspaceDeleted {
				deletion.WorkspaceID = "w"
				deletion.SessionIDs = append(deletion.SessionIDs, "archived")
			}
			v.Model.ApplyArchiveDeletion(deletion)
			ui.Frame()
			if err := ui.Click("返回会话"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if v.Model.SessionID != "" || v.Model.FocusSecondary || workspaceDeleted && v.Model.WorkspaceID != "" {
				t.Fatal("archive return restored a deleted conversation or workspace", v.Model.SessionID, v.Model.WorkspaceID)
			}
			if v.Model.Drafts["home"] != "首页草稿" {
				t.Fatal("unavailable source discarded the independent home draft")
			}
		})
	}
}

func TestArchiveReturnShortcutUsesSourceInsteadOfPreviousSession(t *testing.T) {
	v, ui := archiveNavigationFixture()
	older := domain.Session{ID: "older", WorkspaceID: "w", Title: "更早的会话", Status: "completed"}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, older)
	v.Model.Details[older.ID] = domain.SessionDetail{Session: older}
	v.previousSession, v.previousWorkspace = older.ID, older.WorkspaceID
	enterArchiveFromMenu(t, ui)
	ui.Key(n.Cmd|n.Shift, n.KeyH)
	ui.Advance(time.Second)
	if v.archive.open || v.Model.SessionID != "s" || !ui.HasText("Aligned reply") {
		t.Fatal("return shortcut skipped the archive source for an older conversation", v.Model.SessionID)
	}
}

func TestArchiveDirectReturnKeepsConversationReadingPosition(t *testing.T) {
	v, ui := progressFollowFixture()
	v.lists["s"].ScrollTo(8, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	before, present := ui.Find("进度 8")
	if !present || v.lists["s"].AtEnd() {
		t.Fatal("fixture is not reading earlier history")
	}
	enterArchiveFromMenu(t, ui)
	if err := ui.Click("返回会话"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	after, present := ui.Find("进度 8")
	if !present || v.lists["s"].AtEnd() || math.Abs(float64(after.Y-before.Y)) > 1 {
		t.Fatal("a temporary archive visit reset the conversation's reading position", before, after)
	}
}

// Production View and MyGo native layout, input, Core Text and rasterization.
// Session/worker data are fixtures; recording never changes a user's devices.
func TestArchiveNavigationNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_ARCHIVE_NAV_RECORDING")
	if dir == "" {
		t.Skip("opt-in native archive navigation recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			v, ui := archiveNavigationFixture()
			ui.SetDark(dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			selectArchive := func() {
				t.Helper()
				if err := ui.ChooseMenuItem("已归档…"); err != nil {
					t.Fatal(err)
				}
			}
			for frame := range 360 {
				switch frame {
				case 20, 110, 240:
					click("工作区菜单")
				case 35, 125, 255:
					selectArchive()
				case 60:
					bounds, _ := ui.Find("返回会话")
					ui.Move(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
				case 80, 210:
					click("返回会话")
				case 150:
					click("查看归档会话：归档会话")
				case 180:
					click("返回已归档")
				case 280:
					ui.Key(n.Cmd|n.Shift, n.KeyH)
				case 310:
					click("消息输入")
					ui.Key(n.Cmd, n.KeyA)
					ui.Type("原会话草稿 · 返回后继续输入")
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				for at, file := range map[int]string{50: "archive.png", 100: "returned.png", 165: "history.png", 350: "continued.png"} {
					if frame == at {
						saveNativeSkillImage(t, ui, file)
					}
				}
			}
			if v.Model.SessionID != "s" || v.archive.open || v.Model.Drafts["session:s"] != "原会话草稿 · 返回后继续输入" {
				t.Fatal("native navigation recording did not finish in its original editable conversation")
			}
			narrow := n.NewTester(v.View, 820, 720)
			narrow.SetDark(dark)
			enterArchiveFromMenu(t, narrow)
			saveNativeSkillImage(t, narrow, "narrow.png")
			if err := narrow.Click("返回会话"); err != nil {
				t.Fatal(err)
			}
			narrow.Advance(time.Second)
			saveNativeSkillImage(t, narrow, "narrow-returned.png")
		})
	}
}

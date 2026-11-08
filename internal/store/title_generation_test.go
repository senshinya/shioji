package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
)

func TestGeneratedTitleIsDurableSearchableAndDoesNotReplaceManualRename(t *testing.T) {
	for _, manual := range []bool{false, true} {
		s := open(t)
		w := workspace(t, s)
		op, err := s.Accept(domain.Submission{OperationID: "title", WorkspaceID: w.ID, Text: "请检查红黑树旋转的边界条件"})
		if err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTitleGeneration()
		if err != nil || job.Session.ID != op.SessionID || job.Prompt != "请检查红黑树旋转的边界条件" {
			t.Fatal("new session has no durable title task", job, err)
		}
		if _, err = s.ClaimTitleGeneration(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("one session claimed twice", err)
		}
		if manual {
			if _, err = s.RenameSession(op.SessionID, "我的标题", 0, false); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.FinishTitleGeneration(job, "红黑树旋转边界"); err != nil {
			t.Fatal(err)
		}
		d, _ := s.Session(op.SessionID, 0, 100)
		want := "红黑树旋转边界"
		if manual {
			want = "我的标题"
		}
		if d.Session.Title != want || d.Session.Status != "starting" || len(d.Items) != 1 {
			t.Fatal("title changed conversation or overrode user", d)
		}
		results, err := s.Search(want)
		if err != nil || len(results) != 1 {
			t.Fatal("title not searchable", results, err)
		}
		if err = s.FinishTitleGeneration(job, "迟到标题"); err != nil {
			t.Fatal(err)
		}
		d, _ = s.Session(op.SessionID, 0, 100)
		if d.Session.Title != want {
			t.Fatal("duplicate completion replaced title")
		}
	}
}

func TestTitleTaskSurvivesRestartWithoutReplayingAnUnknownModelRequest(t *testing.T) {
	for _, running := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "state.sqlite")
		s, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		w := workspace(t, s)
		op, err := s.Accept(domain.Submission{OperationID: "restart", WorkspaceID: w.ID, Text: "原始请求"})
		if err != nil {
			t.Fatal(err)
		}
		if running {
			if _, err = s.ClaimTitleGeneration(); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Recover(); err != nil {
			t.Fatal(err)
		}
		_, err = s.ClaimTitleGeneration()
		if running && !errors.Is(err, sql.ErrNoRows) || !running && err != nil {
			t.Fatal("replayed an unknown title request or lost a pending one", running, err)
		}
		d, _ := s.Session(op.SessionID, 0, 100)
		if d.Session.Title != "原始请求" {
			t.Fatal("restart changed fallback title")
		}
		s.Close()
	}
}

func TestGeneratedTitleDoesNotOverrideNativeTitleOrRevisedInput(t *testing.T) {
	for _, source := range []bool{false, true} {
		s := open(t)
		w := workspace(t, s)
		op, _ := s.Accept(domain.Submission{OperationID: "replace", WorkspaceID: w.ID, Text: "旧输入"})
		job, err := s.ClaimTitleGeneration()
		if err != nil {
			t.Fatal(err)
		}
		if source {
			_, err = s.Apply(op.SessionID, domain.Event{Type: "session.title", Text: "源标题"})
		} else {
			_, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: job.InputID, Kind: "user", Text: "修订输入", Final: true}})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = s.FinishTitleGeneration(job, "旧输入的生成标题"); err != nil {
			t.Fatal(err)
		}
		d, _ := s.Session(op.SessionID, 0, 100)
		want := "旧输入"
		if source {
			want = "源标题"
		}
		if d.Session.Title != want {
			t.Fatal("late title replaced revised or named conversation", d.Session.Title)
		}
	}
}

func TestEditingFirstMessageSchedulesNewTitleAndRejectsOldJob(t *testing.T) {
	for _, previousComplete := range []bool{false, true} {
		s := open(t)
		w := workspace(t, s)
		op, err := s.Accept(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "旧输入"})
		if err != nil {
			t.Fatal(err)
		}
		old, err := s.ClaimTitleGeneration()
		if err != nil {
			t.Fatal(err)
		}
		if previousComplete {
			if err = s.FinishTitleGeneration(old, "旧主题"); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:original", Kind: "user", Text: "旧输入", TurnID: "t1", Status: "sent", Final: true}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t1", Status: "completed"}}); err != nil {
			t.Fatal(err)
		}
		d, _ := s.Session(op.SessionID, 0, 100)
		d.Session.ThreadID = "old-thread"
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
			t.Fatal(err)
		}
		d, _ = s.Session(op.SessionID, 0, 100)
		h, err := s.PrepareHistory(domain.HistoryChange{OperationID: "edit", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:original", Restart: true, Text: "改为解释排序算法", Config: d.Session.Config})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetHistoryThread(h.ID, "new-thread"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CommitHistory(h.ID); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTitleGeneration()
		if err != nil {
			t.Fatal("first-message edit did not schedule a new title", err)
		}
		if job.Prompt != "改为解释排序算法" || job.InputID != "input:edit" {
			t.Fatal("new title uses old input", job)
		}
		before, _ := s.Session(op.SessionID, 0, 100)
		if before.Session.Title != d.Session.Title {
			t.Fatal("old title disappeared while generating", before.Session.Title)
		}
		// Even with the replacement job already running, the older result must
		// neither rename the conversation nor consume the replacement job.
		if err = s.FinishTitleGeneration(old, "迟到旧标题"); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishTitleGeneration(job, "排序算法讲解"); err != nil {
			t.Fatal(err)
		}
		after, _ := s.Session(op.SessionID, 0, 100)
		if after.Session.Title != "排序算法讲解" || after.Session.ID != op.SessionID {
			t.Fatal("revised title was lost", after.Session)
		}
		if _, err = s.CommitHistory(h.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimTitleGeneration(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("retry duplicated naming", err)
		}
	}
}

func TestHistoryEditPreservesManualTitleAndLaterMessageTitle(t *testing.T) {
	for _, manual := range []bool{false, true} {
		s := open(t)
		w := workspace(t, s)
		var sid string
		for _, id := range []string{"one", "two"} {
			op, err := s.Accept(domain.Submission{OperationID: id, SessionID: sid, WorkspaceID: w.ID, Text: id})
			if err != nil {
				t.Fatal(err)
			}
			sid = op.SessionID
			if id == "one" {
				job, err := s.ClaimTitleGeneration()
				if err != nil {
					t.Fatal(err)
				}
				if err = s.FinishTitleGeneration(job, "原自动标题"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Apply(sid, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + id, Kind: "user", Text: id, TurnID: id, Status: "sent", Final: true}}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Apply(sid, domain.Event{Type: "turn", Turn: &domain.Turn{ID: id, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
		}
		d, _ := s.Session(sid, 0, 100)
		d.Session.ThreadID = "source"
		if _, err := s.Apply(sid, domain.Event{Type: "session", Session: &d.Session}); err != nil {
			t.Fatal(err)
		}
		item, want := "input:two", "原自动标题"
		if manual {
			if _, err := s.RenameSession(sid, "我的名称", d.Session.TitleRevision, false); err != nil {
				t.Fatal(err)
			}
			item, want = "input:one", "我的名称"
		}
		d, _ = s.Session(sid, 0, 100)
		op, err := s.PrepareHistory(domain.HistoryChange{OperationID: "edit", SessionID: sid, Revision: d.Session.Revision, ItemID: item, Restart: true, Text: "修订", Config: d.Session.Config})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetHistoryThread(op.ID, "revised"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CommitHistory(op.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimTitleGeneration(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("unrelated edit scheduled naming", manual, err)
		}
		after, _ := s.Session(sid, 0, 100)
		if after.Session.Title != want {
			t.Fatal("history edit replaced protected title", after.Session)
		}
	}
}

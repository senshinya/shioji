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

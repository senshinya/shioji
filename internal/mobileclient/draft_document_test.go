package mobileclient

import (
	"testing"

	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestDraftDocumentKeepsReferencesAndUploadsAcrossNativeEdits(t *testing.T) {
	m := app.NewModel()
	key := "session:session"
	m.Drafts[key] = "按  检查  的边界"
	m.DraftSkills[key] = []app.DraftSkill{{
		Reference: domain.SkillReference{ID: "skill", Name: "review-check"}, InlinePosition: 3,
	}}
	m.Attachments[key] = []app.DraftAttachment{
		{Attachment: domain.Attachment{ID: "source", Name: "connection.go", SourcePath: "/work/connection.go"}, Ready: true, InlinePosition: 8},
		{Attachment: domain.Attachment{ID: "upload", Name: "capture.png"}, Transferred: 9},
	}
	chip := func(ref DraftReference) *richtext.Attachment { return &richtext.Attachment{Data: ref} }
	var doc DraftDocument
	doc.Sync(m, key, chip)
	if got := doc.Doc.Text().String(); got != "按 \ufffc 检查 \ufffc 的边界" {
		t.Fatalf("restored the wrong positions: %q", got)
	}
	m.Attachments[key][1].Transferred = 42
	doc.Doc.Edit(func(edit *richtext.Edit) {
		edit.ReplaceString(0, 0, "🙂 ", richtext.Attributes{})
	})
	if !doc.Publish(m, key) || m.Drafts[key] != "🙂 按  检查  的边界" ||
		m.DraftSkills[key][0].InlinePosition != 5 || m.Attachments[key][1].InlinePosition != 10 ||
		m.Attachments[key][0].Attachment.ID != "upload" || m.Attachments[key][0].Transferred != 42 {
		t.Fatalf("editing shifted references or lost the upload: %#v %#v", m.Attachments[key], m.DraftSkills[key])
	}
	doc.Sync(m, key, chip)
	doc.Doc.Undo()
	if !doc.Publish(m, key) || m.DraftSkills[key][0].InlinePosition != 3 || m.Attachments[key][1].InlinePosition != 8 {
		t.Fatalf("undo lost reference positions: %#v %#v", m.Attachments[key], m.DraftSkills[key])
	}
	doc.Doc.Edit(func(edit *richtext.Edit) {
		edit.ReplaceString(2, 3, "", richtext.Attributes{})
	})
	if !doc.Publish(m, key) || len(m.DraftSkills[key]) != 0 || m.Attachments[key][1].InlinePosition != 7 {
		t.Fatalf("deletion did not remove the entire Skill: %#v %#v", m.Attachments[key], m.DraftSkills[key])
	}
	m.Drafts[key] = "authoritative replacement"
	doc.Doc.Undo()
	if doc.Publish(m, key) || m.Drafts[key] != "authoritative replacement" {
		t.Fatal("a stale document overwrote a replaced draft")
	}
}

func TestDepartedDraftStaysRecoverableAndAllowsIndependentEdits(t *testing.T) {
	for _, edit := range []bool{false, true} {
		m := app.NewModel()
		const key = "session:session"
		original := domain.Attachment{ID: "original", Name: "before.png"}
		next := domain.Attachment{ID: "next", Name: "next.png"}
		in := domain.Submission{OperationID: "send", SessionID: "session", Text: "outgoing", Attachments: []domain.Attachment{original}}
		m.Drafts[key] = in.Text
		m.Attachments[key] = []app.DraftAttachment{{Attachment: original}, {Attachment: next}}
		m.Pending[in.OperationID] = in
		var draft DraftDocument
		draft.Sync(m, key, nil)
		draft.Depart(in.OperationID)
		draft.Sync(m, key, nil)
		files := draft.VisibleAttachments(m, key)
		if draft.Doc.Text().String() != "" || m.Drafts[key] != in.Text || len(files) != 1 || files[0].Attachment != next {
			t.Fatalf("outgoing draft must leave the editor and remain recoverable: %q, %+v", draft.Doc.Text().String(), files)
		}
		want := in.Text
		if edit {
			want = "next draft"
			draft.Doc.InsertText(want)
			if !draft.Publish(m, key) || len(m.Attachments[key]) != 1 || m.Attachments[key][0].Attachment != next {
				t.Fatal("publishing the next draft retained an outgoing attachment or lost the next attachment")
			}
		}
		m.Reject(in, "not sent")
		draft.Sync(m, key, nil)
		if got := draft.Doc.Text().String(); got != want {
			t.Fatalf("rejection changed the current draft: got %q, want %q", got, want)
		}
	}
}

func TestUnplacedPathUploadDoesNotRebuildAnEditingDraft(t *testing.T) {
	m := app.NewModel()
	const key = "home"
	m.Drafts[key] = "read it"
	item := domain.Attachment{ID: "pdf", Name: "notes.pdf", Kind: domain.PathAttachmentResource}
	m.Attachments[key] = []app.DraftAttachment{{Attachment: item, DeviceID: "owning-device", WorkspaceID: "source", UploadApproved: true}}
	chip := func(ref DraftReference) *richtext.Attachment { return &richtext.Attachment{Data: ref} }
	var doc DraftDocument
	doc.Sync(m, key, chip)
	doc.Doc.Edit(func(edit *richtext.Edit) { edit.ReplaceString(7, 7, " later", richtext.Attributes{}) })
	m.Attachments[key][0].Ready = true
	doc.Sync(m, key, chip)
	if got := doc.Doc.Text().String(); got != "read it later" {
		t.Fatalf("upload completion rebuilt the editor and replaced unsaved text: %q", got)
	}
	if !doc.Publish(m, key) || m.Drafts[key] != "read it later" || len(m.Attachments[key]) != 1 || !m.Attachments[key][0].Ready {
		t.Fatal("new text and the uploaded file should both survive publishing the draft")
	}
	doc.Doc.SetSelection(7, 4)
	if doc.AdoptPathUploads(m, key, "owning-device", "other", chip) {
		t.Fatal("file was placed in a different workspace")
	}
	if !doc.AdoptPathUploads(m, key, "owning-device", "source", chip) ||
		doc.Doc.Text().String() != "read\ufffc it later" || m.Drafts[key] != "read it later" ||
		len(m.Attachments[key]) != 1 || m.Attachments[key][0].InlinePosition != 5 {
		t.Fatalf("upload should become one inline reference at the active caret: %q, %+v", doc.Doc.Text().String(), m.Attachments[key])
	}
	doc.Doc.Undo()
	if !doc.Publish(m, key) || len(m.Attachments[key]) != 0 || m.Drafts[key] != "read it later" {
		t.Fatal("undo should remove the inline reference while preserving the draft text")
	}
}

func TestInsertDraftCommentAtCaretPreservesSelectedTextAndUndo(t *testing.T) {
	m := app.NewModel()
	const key = "session:right"
	m.Drafts[key] = "ab中文ef"
	m.Drafts["session:left"] = "other draft"
	ref := DraftReference{File: app.DraftAttachment{
		Attachment: domain.Attachment{ID: "comment", Kind: domain.DiffCommentResource}, Ready: true,
	}}
	chip := func(ref DraftReference) *richtext.Attachment { return &richtext.Attachment{Data: ref} }
	var doc DraftDocument
	doc.Sync(m, key, chip)
	doc.Doc.SetSelection(5, 2)
	if !doc.InsertAttachment(m, key, ref, chip) || doc.Doc.Text().String() != "ab\ufffc中文ef" ||
		m.Drafts[key] != "ab中文ef" || m.Drafts["session:left"] != "other draft" {
		t.Fatal("inserting the comment changed the message body or used the wrong caret")
	}
	if got := m.Attachments[key]; len(got) != 1 || got[0].InlinePosition != 3 || got[0].Attachment != ref.File.Attachment {
		t.Fatalf("wrong persisted inline reference: %+v", got)
	}
	if doc.InsertAttachment(m, key, ref, chip) {
		t.Fatal("the same comment was added again")
	}
	doc.Doc.Undo()
	if !doc.Publish(m, key) || len(m.Attachments[key]) != 0 || doc.Doc.Text().String() != "ab中文ef" {
		t.Fatal("undo did not remove only the new comment")
	}
}

func TestReplaceDraftAttachmentKeepsPositionSelectionAndUndo(t *testing.T) {
	m := app.NewModel()
	const key = "session:review"
	m.Drafts[key] = "请核对 后再提交"
	original := domain.Attachment{ID: "old", Name: "rotation.go:28", Kind: domain.DiffCommentResource}
	updated := original
	updated.ID = "new"
	m.Attachments[key] = []app.DraftAttachment{
		{Attachment: original, WorkspaceID: "workspace", DeviceID: "device", InlinePosition: 5, Ready: true},
		{Attachment: domain.Attachment{ID: "upload"}, Transferred: 42},
	}
	chip := func(ref DraftReference) *richtext.Attachment { return &richtext.Attachment{Data: ref} }
	var doc DraftDocument
	doc.Sync(m, key, chip)
	before := doc.Doc.Text().String()
	doc.Doc.SetSelection(8, 2)
	if !doc.ReplaceAttachment(m, key, original, updated, chip) {
		t.Fatal("could not update the original comment")
	}
	got := m.Attachments[key]
	if m.Drafts[key] != "请核对 后再提交" || doc.Doc.Text().String() != before || len(got) != 2 ||
		got[0].Transferred != 42 || got[1].Attachment != updated || got[1].InlinePosition != 5 ||
		got[1].DeviceID != "device" || got[1].WorkspaceID != "workspace" {
		t.Fatalf("replacement moved the comment or changed the draft: %+v", got)
	}
	if anchor, caret := doc.Doc.Selection(); anchor != 8 || caret != 2 {
		t.Fatalf("replacement moved the selection: %d, %d", anchor, caret)
	}
	if doc.ReplaceAttachment(m, key, original, updated, chip) {
		t.Fatal("a late replacement resurrected a retired comment")
	}
	doc.Doc.Undo()
	if !doc.Publish(m, key) || m.Attachments[key][1].Attachment != original {
		t.Fatal("undo did not restore the old comment")
	}
	m.Drafts[key] = "草稿已切换"
	if doc.ReplaceAttachment(m, key, original, updated, chip) || m.Drafts[key] != "草稿已切换" {
		t.Fatal("a stale binding replaced the authoritative draft")
	}
}

func TestReadReferenceQuery(t *testing.T) {
	for _, tt := range []struct {
		text   string
		skills bool
		want   ReferenceQuery
	}{
		{"打开 @internal/worker", true, ReferenceQuery{3, 19, '@', "internal/worker"}},
		{"🙂 \ufffc/review", true, ReferenceQuery{3, 10, '/', "review"}},
		{"打开 @", false, ReferenceQuery{3, 4, '@', ""}},
		{"user@example", true, ReferenceQuery{}},
		{"https://example.test/a", true, ReferenceQuery{}},
		{"/review", false, ReferenceQuery{}},
	} {
		t.Run(tt.text, func(t *testing.T) {
			got, ok := ReadReferenceQuery(tt.text, len([]rune(tt.text)), tt.skills)
			if got != tt.want || ok != (tt.want.Marker != 0) {
				t.Fatalf("got %+v, %v, want %+v", got, ok, tt.want)
			}
		})
	}
}

func TestDraftGoalTagRetainsCommandAcrossRestoreAndUndo(t *testing.T) {
	m := app.NewModel()
	const key = "home"
	m.Drafts[key] = "/go 验证重连"
	chip := func(ref DraftReference) *richtext.Attachment { return &richtext.Attachment{Data: ref} }
	newDraft := func() DraftDocument {
		return DraftDocument{GoalChip: func() *richtext.Attachment { return &richtext.Attachment{Name: "/goal"} }}
	}
	doc := newDraft()
	doc.Sync(m, key, chip)
	doc.InsertGoal(0, 3)
	if !doc.Publish(m, key) || m.Drafts[key] != "/goal 验证重连" || len(m.Attachments[key])+len(m.DraftSkills[key]) != 0 {
		t.Fatalf("selected command lost its wire text or added references: %q", m.Drafts[key])
	}
	if start, end := doc.Doc.Selection(); start != 2 || end != 2 {
		t.Fatalf("objective caret = (%d, %d), want (2, 2)", start, end)
	}
	other := newDraft()
	other.Sync(m, key, chip)
	if got := other.Doc.Text().String(); got != "\ufffc 验证重连" {
		t.Fatalf("restored draft did not use one native text object: %q", got)
	}
	doc.Doc.Undo()
	if !doc.Publish(m, key) || m.Drafts[key] != "/go 验证重连" {
		t.Fatalf("undo did not restore the original input: %q", m.Drafts[key])
	}
}

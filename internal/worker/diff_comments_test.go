package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"testing"
)

func TestPassiveDiffSnapshotsAreBoundedAndCommentsRetainTheirSource(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	var first domain.Attachment
	for i := 0; i < 80; i++ {
		versions := domain.FileVersions{Path: "file.txt", New: strings.Repeat("x", 2048) + strconv.Itoa(i), Comparison: domain.DiffComparison{Repository: dir}}
		a, err := s.storeDiffSnapshot(w.ID, versions)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = a
			_, err = s.captureDiffComment(protocol.DiffCommentInput{GitQuery: protocol.GitQuery{WorkspaceID: w.ID}, Snapshot: a, Comment: "keep", Fragments: []domain.CodeSelection{{Side: "target", Start: 0, End: 1}}})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.attachments.Path(w.ID, first); err != nil {
		t.Fatal("referenced source was evicted", err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.attachments.Directory, w.ID))
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(s.attachments.Directory, w.ID, e.Name()))
		var a domain.Attachment
		json.Unmarshal(data, &a)
		if a.Kind == domain.DiffSnapshotResource {
			count++
		}
	}
	if count > 65 {
		t.Fatalf("passive snapshots are unbounded: %d", count)
	}
}

func TestOpeningCommentRetainsDraftSource(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	first, err := s.storeDiffSnapshot(w.ID, domain.FileVersions{Path: "file.txt", New: "draft", Comparison: domain.DiffComparison{Repository: dir}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gitTestCall(s, "diff.snapshot.retain", map[string]any{"workspaceId": w.ID, "snapshot": first})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		_, err = s.storeDiffSnapshot(w.ID, domain.FileVersions{Path: "file.txt", New: strconv.Itoa(i), Comparison: domain.DiffComparison{Repository: dir}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.attachments.Path(w.ID, first); err != nil {
		t.Fatal("open draft source was evicted", err)
	}
}

func TestPassiveDiffSnapshotsRespectByteBudget(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	for i := 0; i < 15; i++ {
		_, err := s.storeDiffSnapshot(w.ID, domain.FileVersions{Path: "file.txt", Old: strings.Repeat("<", 200<<10), New: strings.Repeat(">", 200<<10) + strconv.Itoa(i), Comparison: domain.DiffComparison{Repository: dir}})
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.attachments.Directory, w.ID))
	var total int64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	if total > 32<<20 {
		t.Fatalf("passive previews retain %d bytes", total)
	}
}

func TestDiffCommentUsesImmutableSelectionWithoutAuxiliaryCode(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("do not include this line\nroot.Right = next.Left\nalso not this line\n"), 0600)
	cmp := compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})
	file, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": cmp["comparison"]})
	if err != nil {
		t.Fatal(err)
	}
	out, err := gitTestCall(s, "diff.comment", map[string]any{"workspaceId": w.ID, "snapshot": file["snapshot"], "comment": "check nil", "fragments": []map[string]any{{"side": "target", "start": 38, "end": 47}}})
	if err != nil {
		t.Fatal(err)
	}
	var ref domain.Attachment
	data, _ := json.Marshal(out)
	json.Unmarshal(data, &ref)
	// Changing the checkout after capture cannot change the model's selection.
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("replaced\n"), 0600)
	inputs, err := s.attachmentInputs(domain.Submission{WorkspaceID: w.ID, Attachments: []domain.Attachment{ref}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(inputs)
	if len(inputs) != 1 || !strings.Contains(string(encoded), "next.Left") || strings.Contains(string(encoded), "do not include") || strings.Contains(string(encoded), "also not") {
		t.Fatalf("model input leaked auxiliary code or lost selection: %s", encoded)
	}
	bad := out
	bad["kind"] = ""
	raw, _ := json.Marshal(bad)
	var forged domain.Attachment
	json.Unmarshal(raw, &forged)
	if _, err = s.attachmentInputs(domain.Submission{WorkspaceID: w.ID, Attachments: []domain.Attachment{forged}}); err == nil {
		t.Fatal("changed resource kind accepted")
	}
}

func TestDiffCommentMixedSelectionLabelsKeepEachSourceSide(t *testing.T) {
	parts := []domain.CodeSelection{{Side: "target", StartLine: 13, EndLine: 13}, {Side: "base", StartLine: 9, EndLine: 9}}
	if got := commentLabel("rotation.go", parts); got != "rotation.go:+13 · −9" {
		t.Fatal("mixed selection mislabeled sources", got)
	}
}

func TestRepeatedDiffReadsReuseImmutableSnapshotStorage(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed\n"), 0600)
	cmp := compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})
	query := map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": cmp["comparison"]}
	first, err := gitTestCall(s, "git.file", query)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gitTestCall(s, "git.file", query)
	if err != nil {
		t.Fatal(err)
	}
	a := first["snapshot"].(map[string]any)
	b := second["snapshot"].(map[string]any)
	if a["id"] != b["id"] {
		t.Fatal("unchanged refresh created a new retained snapshot", a["id"], b["id"])
	}
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed again\n"), 0600)
	third, err := gitTestCall(s, "git.file", query)
	if err != nil {
		t.Fatal(err)
	}
	if third["snapshot"].(map[string]any)["id"] == a["id"] {
		t.Fatal("changed contents reused an old snapshot")
	}
}

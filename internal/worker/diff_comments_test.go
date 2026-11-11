package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"strings"
	"testing"
)

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

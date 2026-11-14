package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedPreferenceSavePreservesPreviousChoice(t *testing.T) {
	u := newUpdater(Options{}, nil)
	u.loadOnce.Do(func() {})
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	os.WriteFile(blocker, []byte("file"), 0600)
	u.file = filepath.Join(blocker, "updater.json")
	err := u.update(func(s *state) { off := false; s.AutomaticChecks = &off })
	if err == nil || !u.automaticChecks() {
		t.Fatal("failed save silently changed update preference", err)
	}
	u.file = filepath.Join(root, "updater.json")
	if err := u.update(func(s *state) { off := false; s.AutomaticChecks = &off }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(u.file)
	if u.automaticChecks() || string(data) != `{"automaticChecks":false}` {
		t.Fatal("preference was not persisted", string(data))
	}
}

func TestReleaseChoiceSaveFailureIsShownAndCanRetry(t *testing.T) {
	u := newUpdater(Options{Language: "zh-Hans"}, func(*session) {})
	u.loadOnce.Do(func() {})
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	os.WriteFile(blocker, []byte("file"), 0600)
	u.file = filepath.Join(blocker, "updater.json")
	s := newSession(u, u.text(), true)
	defer s.cancel()
	r := &release{version: "1.2.3", notes: "new version"}
	done := make(chan struct{})
	go func() { u.offerRelease(s, r, false); close(done) }()
	wait := func(predicate func(view) bool) view {
		t.Helper()
		for {
			v, changed := s.Current()
			if predicate(v) {
				return v
			}
			select {
			case <-changed:
			case <-time.After(time.Minute):
				t.Fatal("update session did not change")
			}
		}
	}
	v := wait(func(v view) bool { return v.Checkbox })
	s.Respond(v.Prompt, actionSkip, true)
	v = wait(func(v view) bool { return v.Detail != "" })
	if !strings.Contains(v.Message, "保存") {
		t.Fatal("preference failure was not explained", v)
	}
	u.mu.Lock()
	if u.state.SkippedVersion != "" || u.state.AutomaticDownloads {
		t.Fatal("failed choice was retained as saved")
	}
	u.file = filepath.Join(root, "updater.json")
	u.mu.Unlock()
	s.Respond(v.Prompt, actionOK, false)
	v = wait(func(v view) bool { return v.Checkbox })
	if !v.Checked {
		t.Fatal("pending checkbox choice lost on retry")
	}
	s.Respond(v.Prompt, actionSkip, true)
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("preference retry did not complete")
	}
	data, _ := os.ReadFile(u.file)
	if !strings.Contains(string(data), `"skippedVersion":"1.2.3"`) {
		t.Fatal("skip choice was not persisted", string(data))
	}
}

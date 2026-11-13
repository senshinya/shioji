package tailnet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInvalidIdentityNeedsUserActionRatherThanRepeatedStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity"), []byte("damaged encrypted identity"), 0600); err != nil {
		t.Fatal(err)
	}
	n := New(dir, func() ([]byte, error) { return make([]byte, 32), nil })
	_, err := n.start("retained", false)
	if !errors.Is(err, ErrUserAction) {
		t.Fatal("damaged identity classified as retryable", err)
	}
}

func TestStartupRetryBacksOffAndStopsForUserAction(t *testing.T) {
	var retry startupRetry
	now := time.Unix(100, 0)
	retry.fail(errors.New("temporary startup failure"), now)
	if retry.due(now) || !retry.due(now.Add(2*time.Second)) {
		t.Fatal("first failure busy loops or never retries")
	}
	retry.fail(errors.New("temporary startup failure"), now.Add(2*time.Second))
	if retry.due(now.Add(3*time.Second)) || !retry.due(now.Add(5*time.Second)) {
		t.Fatal("repeated failure did not back off")
	}
	retry.fail(ErrUserAction, now.Add(5*time.Second))
	if retry.due(now.Add(time.Hour)) {
		t.Fatal("credential failure automatically retried")
	}
	retry.fail(errors.New("temporary startup failure"), now.Add(time.Hour))
	retry.clear()
	if retry.due(now.Add(2 * time.Hour)) {
		t.Fatal("pause did not cancel pending retry")
	}
}

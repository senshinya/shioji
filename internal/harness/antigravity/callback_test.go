package antigravity

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestManagedCallbackUsesBoundSessionAndStableReceipt(t *testing.T) {
	var got []harness.IncomingMessage
	r := &runtime{session: domain.Session{ID: "owned", ThreadID: "native"},
		options: harness.StartOptions{OnMessage: func(_ context.Context, message harness.IncomingMessage) error {
			got = append(got, message)
			return nil
		}}}
	callback, err := r.startCallbackBridge()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = callback.close() })
	post := func(token, body string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, callback.url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	input := `{"id":"stable-event","text":"Review the formatter result"}`
	if post("foreign", input) != http.StatusUnauthorized ||
		post(callback.token, `{"id":"event","text":"forced","sessionId":"foreign"}`) != http.StatusBadRequest ||
		post(callback.token, input) != http.StatusAccepted || post(callback.token, input) != http.StatusAccepted {
		t.Fatal("callback did not enforce transport and ownership")
	}
	if len(got) != 2 || got[0] != got[1] || got[0].SessionID != "owned" || got[0].SourceRef != "native" ||
		got[0].OperationID == "" || got[0].Text != "Review the formatter result" {
		t.Fatalf("callback input was not bound to the original runtime: %+v", got)
	}
}

package codex

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUsageLimitTurnNormalizesProviderReset(t *testing.T) {
	stamp := time.Date(2026, 11, 22, 15, 0, 0, 0, time.Local)
	wantReset := time.Date(2026, 11, 22, 17, 40, 0, 0, time.Local)
	for _, tc := range []struct {
		name, code, message string
		notification        bool
		wantLimit, timed    bool
	}{
		{"turn error", "usageLimitExceeded", "You’ve hit your usage limit. Try again at 5:40 PM.", false, true, true},
		{"terminal notification", "usageLimitExceeded", "You've hit your usage limit. Try again at Nov 22nd, 2026 5:40 PM.", true, true, true},
		{"unknown reset", "usageLimitExceeded", "You've hit your usage limit. Try again later.", false, true, false},
		{"rate limit", "rateLimitExceeded", "Too many requests", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("thread", "session", true)
			feed := func(method string, params map[string]any) []byte {
				t.Helper()
				data, err := json.Marshal(map[string]any{"method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				events, _, err := n.Feed(data)
				if err != nil {
					t.Fatal(err)
				}
				if len(events) == 0 {
					return nil
				}
				out, err := json.Marshal(events[len(events)-1].Turn)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			providerError := map[string]string{"message": tc.message, "codexErrorInfo": tc.code}
			turn := map[string]any{"id": "turn", "status": "failed", "completedAt": stamp.Unix()}
			if tc.notification {
				feed("error", map[string]any{"threadId": "thread", "turnId": "turn", "error": providerError, "willRetry": false})
			} else {
				turn["error"] = providerError
			}
			var got struct {
				UsageLimit *struct{ ResetAt *time.Time }
			}
			if err := json.Unmarshal(feed("turn/completed", map[string]any{"threadId": "thread", "turn": turn}), &got); err != nil {
				t.Fatal(err)
			}
			if (got.UsageLimit != nil) != tc.wantLimit {
				t.Fatalf("usage limit present = %v, want %v", got.UsageLimit != nil, tc.wantLimit)
			}
			if !tc.wantLimit {
				return
			}
			if tc.timed && (got.UsageLimit.ResetAt == nil || !got.UsageLimit.ResetAt.Equal(wantReset)) {
				t.Fatalf("reset = %v, want %v", got.UsageLimit.ResetAt, wantReset)
			}
			if !tc.timed && got.UsageLimit.ResetAt != nil {
				t.Fatalf("invented reset %v", got.UsageLimit.ResetAt)
			}
		})
	}
}

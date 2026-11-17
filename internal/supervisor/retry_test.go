package supervisor

import (
	"testing"
	"time"
)

func TestCrashWindowCountsTenRecentFailuresAndExpiresOldFailures(t *testing.T) {
	now := time.Now()
	r := crashRetry{}
	for i := 0; i < 9; i++ {
		if _, blocked := r.failed(now, time.Time{}); blocked {
			t.Fatal("ordinary circuit opened before ten recent failures", i)
		}
	}
	if _, blocked := r.failed(now, time.Time{}); !blocked {
		t.Fatal("ordinary crash loop was not bounded")
	}
	if _, blocked := r.failed(now.Add(5*time.Minute+time.Nanosecond), time.Time{}); blocked {
		t.Fatal("expired failures still open the circuit")
	}
}

func TestHealthyMinuteResetsCrashWindowAndBackoff(t *testing.T) {
	now := time.Now()
	r := crashRetry{}
	for range 9 {
		r.failed(now, time.Time{})
	}
	attempt, blocked := r.failed(now.Add(time.Minute), now)
	if blocked || attempt != 0 {
		t.Fatal("healthy run did not reset ordinary failures and backoff", attempt, blocked)
	}
}

func TestCrashRetryMatchesManagedRunnerWithoutJitter(t *testing.T) {
	for _, test := range []struct {
		attempt int
		random  float64
		want    time.Duration
	}{
		{0, 0, time.Second},
		{0, 1, time.Second},
		{1, 0.5, 2 * time.Second},
		{4, 0.5, 16 * time.Second},
		{9, 1, time.Minute},
	} {
		if got := crashRetryDelay(test.attempt, 0, 0, test.random); got != test.want {
			t.Fatal(test, got)
		}
	}
}

package mobileclient

import (
	"testing"
	"time"
)

func TestSessionInputDayUsesDeviceCalendarAndUnknownDate(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// The previous calendar day has 23 hours when daylight saving starts.
	now := time.Date(2026, 3, 9, 0, 10, 0, 0, location)
	tests := []struct {
		instant time.Time
		want    SessionInputDay
	}{
		{time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC), SessionInputToday},
		{time.Date(2026, 3, 9, 3, 59, 0, 0, time.UTC), SessionInputYesterday},
		{time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), SessionInputYesterday},
		{time.Date(2026, 3, 8, 4, 59, 0, 0, time.UTC), SessionInputEarlier},
		{time.Time{}, SessionInputUnknown},
		{time.Date(2026, 3, 10, 4, 0, 0, 0, time.UTC), SessionInputUnknown},
	}
	for _, test := range tests {
		if got := InputDay(test.instant, now); got != test.want {
			t.Errorf("bucket of %v on %v = %v, want %v", test.instant, now, got, test.want)
		}
	}
}

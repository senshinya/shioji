package domain

import (
	"testing"
	"time"
)

func TestScheduleCalendarUsesDeviceClockAcrossDaylightSaving(t *testing.T) {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	rule := ScheduleRule{Kind: "daily", Time: "02:30"}
	after := time.Date(2026, 3, 7, 2, 30, 0, 0, zone)
	if want, got := time.Date(2026, 3, 9, 2, 30, 0, 0, zone), rule.Next(after, zone); !got.Equal(want) {
		t.Fatal("spring gap changed the chosen wall clock", got, want)
	}
	after = time.Date(2026, 10, 31, 2, 30, 0, 0, zone)
	rule.Time = "01:30"
	fold := rule.Next(after, zone)
	if next := rule.Next(fold, zone).In(zone); fold.In(zone).Day() != 1 || next.Day() != 2 || next.Hour() != 1 {
		t.Fatal("wall-clock slot was counted twice", fold, next)
	}
}

func TestScheduleIntervalKeepsItsOriginalPhaseAfterDowntime(t *testing.T) {
	rule := ScheduleRule{Kind: "interval", Minutes: 90}
	first := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	latest, next := rule.Advance(first, first.Add(5*time.Hour), time.UTC)
	if !latest.Equal(first.Add(270*time.Minute)) || !next.Equal(first.Add(360*time.Minute)) {
		t.Fatal(latest, next)
	}
}

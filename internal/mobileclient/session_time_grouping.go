package mobileclient

import "time"

type SessionInputDay uint8

const (
	SessionInputToday SessionInputDay = iota
	SessionInputYesterday
	SessionInputEarlier
	SessionInputUnknown
)

func StartOfDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func InputDay(stamp, now time.Time) SessionInputDay {
	today := StartOfDay(now)
	switch {
	case stamp.IsZero() || !stamp.Before(today.AddDate(0, 0, 1)):
		return SessionInputUnknown
	case !stamp.Before(today):
		return SessionInputToday
	case !stamp.Before(today.AddDate(0, 0, -1)):
		return SessionInputYesterday
	default:
		return SessionInputEarlier
	}
}

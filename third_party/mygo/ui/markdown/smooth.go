package markdown

import (
	"math"
	"time"
	"unicode/utf8"
)

// Rate estimates use Unicode characters rather than bytes, so Chinese prose
// and ASCII code have the same timing. Only new input and released text are
// scanned; the settled prefix is never walked on an animation frame.
type revealCadence struct {
	bytes, pending int
	inputAt        time.Time
	rate, chunk    float64
	budget         float64
	drainBy        time.Time
}

func (rs *revealState) smoothStep(now time.Time, src string, streaming bool) (int, bool) {
	s := &rs.cadence
	if s.inputAt.IsZero() {
		s.bytes, s.rate = rs.shown, 38
	}
	if len(src) > s.bytes {
		added := utf8.RuneCountInString(src[s.bytes:])
		if !s.inputAt.IsZero() {
			dt := now.Sub(s.inputAt).Seconds()
			if dt > 0 && dt < 1 {
				s.rate += .2 * (float64(added)/dt - s.rate)
			}
		}
		if s.chunk == 0 {
			s.chunk = float64(added)
		} else {
			s.chunk += .2 * (float64(added) - s.chunk)
		}
		s.pending += added
		s.bytes, s.inputAt, s.drainBy = len(src), now, time.Time{}
	}
	if s.pending == 0 {
		rs.last = now
		return rs.shown, false
	}
	dt := now.Sub(rs.last)
	if rs.last.IsZero() || dt > 100*time.Millisecond {
		dt = time.Second / 60
	}
	rs.last = now
	idle := now.Sub(s.inputAt)
	rate := max(18, s.rate)
	available := s.pending
	if streaming && idle < 220*time.Millisecond {
		// Preserve enough received text to bridge the next ordinary input
		// interval. Pressure accelerates large bursts without a fixed CPS cap.
		lag := max(1, int(math.Round(rate*.12+min(s.chunk*.35, rate*.12))))
		available = max(0, s.pending-lag)
		if rs.shown == 0 {
			available = max(1, available)
		}
		rate += float64(max(0, available)) * 3
	} else {
		rate = max(120, rate*1.8)
		if !streaming || idle >= 360*time.Millisecond {
			if s.drainBy.IsZero() {
				drain := max(180*time.Millisecond, min(520*time.Millisecond, time.Duration(s.pending)*8*time.Millisecond))
				s.drainBy = now.Add(drain)
			}
			remaining := max(dt, s.drainBy.Sub(now))
			rate = max(rate, float64(s.pending)/remaining.Seconds())
		}
	}
	s.budget += rate * dt.Seconds()
	count := min(available, int(s.budget))
	if rs.shown == 0 {
		count = max(1, count)
	}
	if count == 0 {
		s.budget = min(s.budget, 1)
		return rs.shown, true
	}
	s.budget -= float64(count)
	for i := 0; i < count; i++ {
		// Stagger characters within this frame as well, instead of fading
		// an entire newly delivered chunk with one common birth timestamp.
		birth := now.Add(-time.Duration(count-1-i) * dt / time.Duration(count))
		rs.marks = append(rs.marks, revealMark{rs.shown, birth})
		_, size := utf8.DecodeRuneInString(src[rs.shown:])
		rs.shown += size
	}
	s.pending -= count
	return rs.shown, s.pending > 0
}

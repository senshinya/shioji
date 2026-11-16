package supervisor

import "time"

// Lody's ordinary crash policy: ten failures in five minutes, reset after a
// minute of healthy service. The window belongs to this supervisor instance.
type crashRetry struct {
	failures []time.Time
	attempt  int
}

func (r *crashRetry) failed(now, healthySince time.Time) (int, bool) {
	if !healthySince.IsZero() && now.Sub(healthySince) >= time.Minute {
		*r = crashRetry{}
	}
	cutoff := now.Add(-5 * time.Minute)
	for len(r.failures) > 0 && r.failures[0].Before(cutoff) {
		r.failures = r.failures[1:]
	}
	r.failures = append(r.failures, now)
	attempt := r.attempt
	r.attempt++
	return attempt, len(r.failures) >= 10
}

func crashRetryDelay(attempt int, minimum, maximum time.Duration, random float64) time.Duration {
	if minimum <= 0 {
		minimum = time.Second
	}
	if maximum <= 0 {
		maximum = time.Minute
	}
	delay := minimum
	for i := 0; i < attempt && delay < maximum; i++ {
		delay = min(delay*2, maximum)
	}
	delay = time.Duration(float64(delay) * (0.8 + random*0.4))
	return min(delay, maximum)
}

package tailnet

import (
	"errors"
	"math/rand/v2"
	"time"
)

type startupRetry struct {
	at       time.Time
	attempts int
}

func (r *startupRetry) clear()                 { r.at = time.Time{}; r.attempts = 0 }
func (r *startupRetry) due(now time.Time) bool { return !r.at.IsZero() && !now.Before(r.at) }
func (r *startupRetry) fail(err error, now time.Time) {
	if errors.Is(err, ErrUserAction) || errors.Is(err, ErrOwned) {
		r.clear()
		return
	}
	d := time.Second << min(r.attempts, 5)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	r.at = now.Add(time.Duration(float64(d) * (0.9 + rand.Float64()*0.2)))
	r.attempts = min(r.attempts+1, 6)
}

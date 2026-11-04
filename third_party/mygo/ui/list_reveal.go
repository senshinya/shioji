package ui

// ListReveal clips a contiguous range [First, End) to Progress of its natural
// height. Rows keep their full content layout; only the last exposed row clips.
// Supply sorted, nonoverlapping ranges. Hidden suffix rows remain virtualized.
type ListReveal struct {
	First, End int
	Progress   float32
}

func (hs *listHeights) top(i int, gap float64) float64 {
	position := hs.naturalTop(i, gap)
	y := position
	for _, r := range hs.reveals {
		if i <= r.First {
			break
		}
		start, end := hs.naturalTop(r.First, gap), hs.naturalTop(r.End, gap)
		visible := (end - start) * float64(max(0, min(1, r.Progress)))
		y -= max(0, min(position-start, end-start)-visible)
	}
	return y
}

func (hs *listHeights) height(i int) float64 {
	height := hs.naturalHeight(i)
	for _, r := range hs.reveals {
		if i < r.First {
			break
		}
		if i < r.End {
			start := hs.naturalTop(r.First, hs.gap)
			visible := (hs.naturalTop(r.End, hs.gap) - start) * float64(max(0, min(1, r.Progress)))
			return max(0, min(height, start+visible-hs.naturalTop(i, hs.gap)))
		}
	}
	return height
}

func (hs *listHeights) step(i int, gap float64) float64 {
	if len(hs.reveals) == 0 {
		return hs.naturalHeight(i) + gap
	}
	return hs.top(i+1, gap) - hs.top(i, gap)
}

// Skip the hidden suffix in one step rather than mounting zero-height rows.
func (hs *listHeights) nextVisible(i, direction int) int {
	for _, r := range hs.reveals {
		if i < r.First {
			break
		}
		if i < r.End && hs.height(i) == 0 {
			if direction > 0 {
				i = r.End
				continue
			}
			limit := hs.naturalTop(r.First, hs.gap) + (hs.naturalTop(r.End, hs.gap)-hs.naturalTop(r.First, hs.gap))*float64(max(0, min(1, r.Progress)))
			lo, hi := r.First, r.End
			for lo < hi {
				mid := (lo + hi) / 2
				if hs.naturalTop(mid, hs.gap) < limit {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			return lo - 1
		}
	}
	return i
}

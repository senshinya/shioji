package ui

// ListReveal clips a contiguous range [First, End) to Progress of its natural
// height. Rows keep their full content layout; the boundary row clips.
// Supply sorted, nonoverlapping ranges. Hidden rows remain virtualized.
type ListReveal struct {
	First, End int
	Progress   float32
	// MaxHeight limits the animated height while Progress < 1. Use the
	// viewport height for long disclosures: offscreen rows should not consume
	// most of the animation. At Progress >= 1 the full range is available.
	// Zero preserves the natural-height animation.
	MaxHeight float32
	// FromEnd exposes the trailing rows. Use it when a trailing disclosure
	// closes content above itself; the latest rows stay next to the control.
	FromEnd bool
	// LayoutOnly changes the occupied height while keeping the row mounted
	// and unclipped, even at zero height. Use it for a single row containing
	// a lifted element whose motion is independent of the reveal.
	LayoutOnly bool
}

func (hs *listHeights) revealedHeight(r ListReveal, gap float64) float64 {
	height := hs.naturalTop(r.End, gap) - hs.naturalTop(r.First, gap)
	if r.MaxHeight > 0 && r.Progress < 1 {
		height = min(height, float64(r.MaxHeight))
	}
	return height * float64(max(0, min(1, r.Progress)))
}

func (hs *listHeights) top(i int, gap float64) float64 {
	position := hs.naturalTop(i, gap)
	y := position
	for _, r := range hs.reveals {
		if i <= r.First {
			break
		}
		start, end := hs.naturalTop(r.First, gap), hs.naturalTop(r.End, gap)
		visible := hs.revealedHeight(r, gap)
		if r.FromEnd {
			y -= min(position-start, end-start-visible)
		} else {
			y -= max(0, min(position-start, end-start)-visible)
		}
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
			visible := hs.revealedHeight(r, hs.gap)
			if r.FromEnd {
				cut := hs.naturalTop(r.End, hs.gap) - visible
				return max(0, height-max(0, cut-hs.naturalTop(i, hs.gap)))
			}
			return max(0, min(height, start+visible-hs.naturalTop(i, hs.gap)))
		}
	}
	return height
}

// The partially exposed leading row keeps its bottom content, not its top.
func (hs *listHeights) revealOffset(i int) float64 {
	for _, r := range hs.reveals {
		if i < r.First {
			break
		}
		if r.FromEnd && !r.LayoutOnly && i < r.End {
			cut := hs.naturalTop(r.End, hs.gap) - hs.revealedHeight(r, hs.gap)
			return max(0, min(hs.naturalHeight(i), cut-hs.naturalTop(i, hs.gap)))
		}
	}
	return 0
}

func (hs *listHeights) revealClips(i int) bool {
	for _, r := range hs.reveals {
		if i < r.First {
			break
		}
		if i < r.End {
			return !r.LayoutOnly
		}
	}
	return false
}

func (hs *listHeights) step(i int, gap float64) float64 {
	if len(hs.reveals) == 0 {
		return hs.naturalHeight(i) + gap
	}
	return hs.top(i+1, gap) - hs.top(i, gap)
}

// Skip hidden rows rather than mounting a zero-height prefix or suffix.
func (hs *listHeights) nextVisible(i, direction int) int {
	// An empty row is not a clipped prefix or suffix. Searching the reveal's
	// boundary for it can move against direction and trap layout in a cycle.
	if hs.naturalHeight(i) == 0 {
		return i
	}
	for _, r := range hs.reveals {
		if i < r.First {
			break
		}
		if i < r.End && !r.LayoutOnly && hs.height(i) == 0 {
			if r.FromEnd {
				if direction < 0 {
					return r.First - 1
				}
				cut := hs.naturalTop(r.End, hs.gap) - hs.revealedHeight(r, hs.gap)
				lo, hi := r.First, r.End
				for lo < hi {
					mid := (lo + hi) / 2
					if hs.naturalTop(mid, hs.gap)+hs.naturalHeight(mid) <= cut {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				return lo
			}
			if direction > 0 {
				i = r.End
				continue
			}
			limit := hs.naturalTop(r.First, hs.gap) + hs.revealedHeight(r, hs.gap)
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

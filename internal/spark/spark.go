// Package spark renders numeric series as single-line Unicode block
// sparklines — stdlib only, no dependency. A NaN marks a gap (no data for
// that bucket) and renders as a space so missing weeks read as holes rather
// than zeros.
package spark

import (
	"math"
	"strings"
)

var blocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Line maps each value to one of eight block heights, scaled to the series'
// own min..max. A flat (all-equal) non-zero series renders at mid height; an
// all-zero or empty series renders flat at the floor. NaN values render as a
// space. The scale is per-series, so a sparkline shows shape, not magnitude —
// pair it with the numeric endpoints when absolute level matters.
func Line(vals []float64) string {
	if len(vals) == 0 {
		return ""
	}
	min, max := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if math.IsInf(min, 1) { // every value was NaN
		return strings.Repeat(" ", len(vals))
	}
	rng := max - min
	var b strings.Builder
	for _, v := range vals {
		switch {
		case math.IsNaN(v):
			b.WriteRune(' ')
		case rng == 0:
			if max == 0 {
				b.WriteRune(blocks[0])
			} else {
				b.WriteRune(blocks[(len(blocks)-1)/2])
			}
		default:
			idx := int((v-min)/rng*float64(len(blocks)-1) + 0.5)
			b.WriteRune(blocks[idx])
		}
	}
	return b.String()
}

// Trend returns an arrow for the series' direction from its first to its last
// non-NaN value: "↑" rising, "↓" falling, "→" flat or fewer than two points.
func Trend(vals []float64) string {
	first, last, have := math.NaN(), math.NaN(), 0
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		if have == 0 {
			first = v
		}
		last = v
		have++
	}
	switch {
	case have < 2 || last == first:
		return "→"
	case last > first:
		return "↑"
	default:
		return "↓"
	}
}

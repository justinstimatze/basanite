package display

import "strings"

// SplitPending buffers a streamed delta so Apply/ApplyWithGlyphs only ever
// see complete lines. MessageDisplay's batches are not line-aligned — a tic,
// or a fence marker, can land split across two deltas — so delta is combined
// with whatever pending held back from the previous call, and everything
// through the last newline is released as safe while the remainder is held
// as the new pending. On the message's final batch, everything flushes
// regardless of a trailing newline: a message can end mid-line, and that
// last partial line must still display or it silently vanishes.
//
// Cost: a long line streamed across many small deltas with no newline in it
// displays nothing until one arrives, or the message ends — a bounded
// latency tradeoff for never swapping inside a fragment.
func SplitPending(delta, pending string, final bool) (safe, newPending string) {
	combined := pending + delta
	if final {
		return combined, ""
	}
	i := strings.LastIndexByte(combined, '\n')
	if i < 0 {
		return "", combined
	}
	return combined[:i+1], combined[i+1:]
}

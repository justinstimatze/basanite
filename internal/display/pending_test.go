package display

import "testing"

func TestSplitPendingHoldsBackAPartialLine(t *testing.T) {
	safe, pending := SplitPending("load-bea", "", false)
	if safe != "" || pending != "load-bea" {
		t.Errorf("no newline yet: got (%q, %q), want (\"\", \"load-bea\")", safe, pending)
	}
}

func TestSplitPendingReleasesThroughTheLastNewline(t *testing.T) {
	safe, pending := SplitPending("ring is up.\nNext line starts", "load-bea", false)
	if want := "load-bearing is up.\n"; safe != want {
		t.Errorf("safe = %q, want %q", safe, want)
	}
	if pending != "Next line starts" {
		t.Errorf("pending = %q, want %q", pending, "Next line starts")
	}
}

func TestSplitPendingFlushesEverythingOnFinalEvenWithoutANewline(t *testing.T) {
	safe, pending := SplitPending(" trailing", "no newline here", true)
	if want := "no newline here trailing"; safe != want {
		t.Errorf("safe = %q, want %q", safe, want)
	}
	if pending != "" {
		t.Errorf("pending after final must be empty, got %q", pending)
	}
}

// The guard bug check-plan's trace caught: Claude Code sends a final batch
// with an empty delta when the message ends on a newline. If that case
// doesn't reach SplitPending at all, whatever's held in pending never
// flushes — silent data loss on a routine, documented input shape.
func TestSplitPendingFlushesPendingOnAFinalBatchWithEmptyDelta(t *testing.T) {
	safe, pending := SplitPending("", "load-bearing checks", true)
	if safe != "load-bearing checks" {
		t.Errorf("safe = %q, want the held-back text flushed", safe)
	}
	if pending != "" {
		t.Errorf("pending after final must be empty, got %q", pending)
	}
}

// Mid-word and mid-fence splits are the two real failure shapes this fix
// exists for — proved here against Apply/ApplyWithGlyphs directly, not just
// against SplitPending in isolation.
func TestSplitPendingThenApplyFixesAWordSplitAcrossTwoBatches(t *testing.T) {
	s := Swaps{"load-bearing": "supporting"}
	st := State{}

	safe, pending := SplitPending("This check is load-bea", st.Pending, false)
	st.Pending = pending
	out1, st, _ := s.Apply(safe, st)
	if out1 != "" {
		t.Errorf("no newline yet, nothing should display: got %q", out1)
	}

	safe, pending = SplitPending("ring.\n", st.Pending, false)
	st.Pending = pending
	out2, _, counts := s.Apply(safe, st)
	if want := "This check is supporting.\n"; out2 != want {
		t.Errorf("got %q, want %q", out2, want)
	}
	if counts["load-bearing"] != 1 {
		t.Errorf("the swap split across batches must still count: %v", counts)
	}
}

func TestSplitPendingThenApplyFixesAFenceMarkerSplitAcrossTwoBatches(t *testing.T) {
	s := Swaps{"substrate": "component"}
	st := State{}

	safe, pending := SplitPending("Here is code:\n``", st.Pending, false)
	st.Pending = pending
	_, st, _ = s.Apply(safe, st)
	if st.InFence {
		t.Fatal("a fence marker split mid-backtick must not register as a fence yet")
	}

	safe, pending = SplitPending("`\nx := \"substrate\"\n", st.Pending, false)
	st.Pending = pending
	out, st, _ := s.Apply(safe, st)
	if !st.InFence {
		t.Fatal("the completed fence marker must register once whole")
	}
	if out != "```\nx := \"substrate\"\n" {
		t.Errorf("code inside the now-open fence was rewritten: %q", out)
	}
}

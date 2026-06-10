package detect

import "testing"

func TestRankConcentrationBeatsFlat(t *testing.T) {
	recent := map[string]int{"tic": 50, "common": 500}
	baseline := map[string]int{"tic": 2, "common": 900}
	perProject := map[string]map[string]int{
		"tic":    {"a": 20, "b": 15, "c": 15},
		"common": {"a": 200, "b": 150, "c": 150},
	}
	got := Rank(recent, perProject, baseline, 10000, 20000, 5, 1.0, 0)
	if len(got) == 0 || got[0].Lemma != "tic" {
		t.Fatalf("tic should outrank flat common word: %+v", got)
	}
	// at the default floor the 1.1x flat word is cut entirely
	if got := Rank(recent, perProject, baseline, 10000, 20000, 5, 2.0, 0); len(got) != 1 || got[0].Lemma != "tic" {
		t.Errorf("with ratio floor 2.0 only the tic should survive: %+v", got)
	}
}

func TestRankLeaveLoudestOut(t *testing.T) {
	recent := map[string]int{"topicword": 100, "dictionword": 30}
	baseline := map[string]int{}
	perProject := map[string]map[string]int{
		"topicword":   {"oneproject": 100},
		"dictionword": {"a": 10, "b": 10, "c": 10},
	}
	got := Rank(recent, perProject, baseline, 10000, 20000, 5, 1.0, 0)
	if len(got) != 1 || got[0].Lemma != "dictionword" {
		t.Fatalf("single-project topic word must be excluded entirely: %+v", got)
	}
	if got[0].OutsideCount != 20 {
		t.Errorf("OutsideCount = %d, want 20 (30 minus loudest 10)", got[0].OutsideCount)
	}
}

func TestRankRatioFloor(t *testing.T) {
	recent := map[string]int{"drifter": 60}
	baseline := map[string]int{"drifter": 80} // ratio ~1.5 at equal totals
	perProject := map[string]map[string]int{"drifter": {"a": 30, "b": 30}}
	if got := Rank(recent, perProject, baseline, 10000, 20000, 5, 2.0, 0); len(got) != 0 {
		t.Errorf("ratio floor 2.0 should cut a 1.5x drifter: %+v", got)
	}
}

func TestRankEmptyWindows(t *testing.T) {
	if got := Rank(map[string]int{"x": 9}, nil, nil, 100, 0, 1, 1.0, 0); got != nil {
		t.Errorf("empty baseline window must return nil, got %+v", got)
	}
}

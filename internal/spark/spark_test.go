package spark

import (
	"math"
	"testing"
)

func TestLine(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want string
	}{
		{"empty", nil, ""},
		{"rising", []float64{0, 1, 2, 3}, "▁▃▆█"},
		{"falling", []float64{3, 2, 1, 0}, "█▆▃▁"},
		{"flat nonzero", []float64{2, 2, 2}, "▄▄▄"},
		{"all zero", []float64{0, 0, 0}, "▁▁▁"},
		{"gap renders as space", []float64{1, math.NaN(), 2}, "▁ █"},
		{"all gaps", []float64{math.NaN(), math.NaN()}, "  "},
		{"single", []float64{5}, "▄"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Line(c.in); got != c.want {
				t.Errorf("Line(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTrend(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want string
	}{
		{"rising", []float64{1, 2, 3}, "↑"},
		{"falling", []float64{3, 2, 1}, "↓"},
		{"flat", []float64{2, 2, 2}, "→"},
		{"single point", []float64{4}, "→"},
		{"gaps ignored, net rise", []float64{1, math.NaN(), 5}, "↑"},
		{"endpoints only, dips between", []float64{2, 9, 2}, "→"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Trend(c.in); got != c.want {
				t.Errorf("Trend(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

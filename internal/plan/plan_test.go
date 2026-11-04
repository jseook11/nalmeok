package plan

import (
	"slices"
	"testing"

	"github.com/jseook11/nalmeok/internal/media"
)

func pieces(seconds ...float64) []media.Piece {
	out := make([]media.Piece, len(seconds))
	for i, s := range seconds {
		out[i] = media.Piece{Path: string(rune('a' + i)), MIME: "audio/m4a", Seconds: s}
	}
	return out
}

// file returns the pieces of one source file, named a, b, c… in order.
func file(seconds ...float64) []media.Piece { return pieces(seconds...) }

func TestUnits(t *testing.T) {
	const m = UnitMaxSeconds
	cases := []struct {
		name   string
		in     [][]float64
		groups [][]string
		secs   []float64
	}{
		{"none", nil, nil, nil},
		{"one", [][]float64{{60}}, [][]string{{"a"}}, []float64{60}},
		{"fits exactly", [][]float64{{m / 2, m / 2}}, [][]string{{"a", "b"}}, []float64{m}},
		{"overflow", [][]float64{{m / 2, m / 2, 61}}, [][]string{{"a", "b"}, {"c"}}, []float64{m, 61}},
		// A cut's last few seconds ride along instead of costing a request.
		{"remainder folds in", [][]float64{{m / 2, m / 2, 30}}, [][]string{{"a", "b", "c"}}, []float64{m + 30}},
		{"greedy in order", [][]float64{{3000, 1200, 600, m, 100}}, [][]string{{"a"}, {"b", "c"}, {"d"}, {"e"}}, []float64{3000, 1800, m, 100}},
		{"long piece alone", [][]float64{{10, m + 100, 10}}, [][]string{{"a"}, {"b"}, {"c"}}, []float64{10, m + 100, 10}},
		{"split file pieces", [][]float64{{m, m, 1800}}, [][]string{{"a"}, {"b"}, {"c"}}, []float64{m, m, 1800}},
		// Two source files never share a request, even when they would fit.
		{"two short files", [][]float64{{60}, {60}}, [][]string{{"a"}, {"a"}}, []float64{60, 60}},
		{"folder of files", [][]float64{{m}, {m, m}, {1800}}, [][]string{{"a"}, {"a"}, {"b"}, {"a"}}, []float64{m, m, m, 1800}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracks := make([][]media.Piece, 0, len(c.in))
			for _, seconds := range c.in {
				tracks = append(tracks, file(seconds...))
			}
			got := Units(tracks, m)
			if len(got) != len(c.groups) {
				t.Fatalf("units = %d, want %d", len(got), len(c.groups))
			}
			for i, u := range got {
				var names []string
				for _, p := range u.Pieces {
					names = append(names, p.Path)
				}
				if u.Index != i || !slices.Equal(names, c.groups[i]) || u.Seconds != c.secs[i] {
					t.Errorf("unit %d = {%d %v %v}, want {%d %v %v}", i, u.Index, names, u.Seconds, i, c.groups[i], c.secs[i])
				}
			}
		})
	}
}

// TestUnitsOneFilePerRequest is the 2026-11-02 rule: a folder of N files
// always yields at least N requests and no request carries two files.
func TestUnitsOneFilePerRequest(t *testing.T) {
	tracks := [][]media.Piece{
		file(10 * 60),
		file(5 * 60),
		file(UnitMaxSeconds, 600), // a long file already split by media.Prepare
	}
	units := Units(tracks, UnitMaxSeconds)
	if len(units) != 4 {
		t.Fatalf("units = %d, want 4", len(units))
	}
	for _, u := range units {
		if len(u.Pieces) != 1 {
			t.Fatalf("unit %d has %d pieces, want 1", u.Index, len(u.Pieces))
		}
	}
}

func TestEvenSeconds(t *testing.T) {
	const m = UnitMaxSeconds
	cases := []struct {
		seconds, want float64
	}{
		{0, m},
		{30 * 60, m},
		{m, m},                     // fits: not cut
		{110 * 60, 55*60 + 1},      // two halves, not 65 + 45
		{130 * 60, 65*60 + 1},      // exactly two requests
		{140 * 60, 140*20 + 1},     // three of 46m40s
		{3*m + 1, (3*m+1)/4.0 + 1}, // just over three: four even pieces
	}
	for _, c := range cases {
		if got := EvenSeconds(c.seconds, m); got != c.want {
			t.Errorf("EvenSeconds(%v) = %v, want %v", c.seconds, got, c.want)
		}
	}
	if got := EvenSeconds(5000, 0); got != 0 {
		t.Errorf("no limit = %v, want 0", got)
	}
}

// TestEvenPiecesMakeEvenUnits cuts like media.Prepare (pieces of at most the
// even length, the last one short by frame rounding) and checks the units
// come out the requested count.
func TestEvenPiecesMakeEvenUnits(t *testing.T) {
	for _, total := range []float64{110 * 60, 130 * 60, 140 * 60, 200 * 60} {
		cut := EvenSeconds(total, UnitMaxSeconds) - 0.05 // a frame short
		var secs []float64
		for left := total; left > 0; left -= cut {
			secs = append(secs, min(cut, left))
		}
		units := Units([][]media.Piece{file(secs...)}, UnitMaxSeconds)
		if want := RequestCount(total); len(units) != want {
			t.Errorf("%v min: %d units, want %d", total/60, len(units), want)
		}
	}
}

func TestRequestCount(t *testing.T) {
	cases := map[float64]int{0: 0, 1: 1, UnitMaxSeconds: 1, UnitMaxSeconds + 1: 2, 3 * UnitMaxSeconds: 3}
	for in, want := range cases {
		if got := RequestCount(in); got != want {
			t.Errorf("RequestCount(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestEstimateKRW(t *testing.T) {
	// Python: audio 25 tok/s, prompt 1989//4 tokens per request, output
	// 2.5–3.0 tok/s, gemini-3.8-flash (0.75, 0.75, 3.75)/1M, Batch ×0.5, 1400원.
	cases := []struct {
		seconds   float64
		batch     bool
		low, high int
	}{
		{0, true, 0, 0},
		{3600, true, 71, 76},    // spec: 약 74원/시간
		{3600, false, 142, 152}, // live (paid) price
		{5400, true, 107, 114},
		{10 * 3600, true, 711, 759}, // 10 requests of an hour
	}
	for _, c := range cases {
		low, high := EstimateKRW(c.seconds, c.batch)
		if low != c.low || high != c.high {
			t.Errorf("EstimateKRW(%v, %v) = %d, %d; want %d, %d", c.seconds, c.batch, low, high, c.low, c.high)
		}
	}
}

// Package plan groups prepared audio pieces into requests and estimates cost.
// Everything here is a pure function (spec §5-3).
package plan

import (
	"math"

	"github.com/jseook11/nalmeok/internal/media"
)

// UnitMaxSeconds is the longest audio one request may carry (130 minutes).
const UnitMaxSeconds = 130 * 60

// Pricing for gemini-3.8-flash, USD per 1M tokens (nalmeok/constants.py MODELS).
const (
	priceTextIn  = 0.75
	priceAudioIn = 0.75
	priceOutput  = 3.75

	batchMultiplier = 0.5
	usdToKRW        = 1400

	// AudioTokensPerSecond is the measured audio token rate of gemini-3.8-flash.
	AudioTokensPerSecond = 25
	outputLowPerSecond   = 2.5
	outputHighPerSecond  = 3.0

	// promptTextTokens approximates the PROMPT text (1989 chars / 4), sent
	// once per request.
	promptTextTokens = 1989 / 4
)

// UnitPlan is one request: pieces in time order.
type UnitPlan struct {
	Index   int // 0-based; Batch key is "unit-<Index>"
	Pieces  []media.Piece
	Seconds float64
}

// Units plans one request per source file. tracks holds the prepared pieces of
// each source file in order. Pieces of one file are packed greedily up to
// maxSeconds; a file longer than maxSeconds becomes several units. Pieces of
// different files are never put in the same request: Gemini blocked a folder
// whose two lectures shared one request (2026-11-02 measurement), while each
// file alone passed.
func Units(tracks [][]media.Piece, maxSeconds float64) []UnitPlan {
	var units []UnitPlan
	for _, pieces := range tracks {
		first := len(units) // first unit of this file
		for _, p := range pieces {
			n := len(units)
			if n == first || units[n-1].Seconds+p.Seconds > maxSeconds+1e-6 {
				units = append(units, UnitPlan{Index: n})
				n++
			}
			u := &units[n-1]
			u.Pieces = append(u.Pieces, p)
			u.Seconds += p.Seconds
		}
	}
	return units
}

// RequestCount is how many requests seconds of audio needs at UnitMaxSeconds.
func RequestCount(seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	return int(math.Ceil(seconds / UnitMaxSeconds))
}

// EstimateKRW ports the Python estimate: audio 25 tok/s, output 2.5–3.0
// tok/s, prompt text once per request; Batch is half price. Returns the
// rounded won range.
func EstimateKRW(seconds float64, batch bool) (low, high int) {
	if seconds <= 0 {
		return 0, 0
	}
	audio := math.Floor(seconds * AudioTokensPerSecond)
	text := float64(promptTextTokens * RequestCount(seconds))
	factor := 1.0
	if batch {
		factor = batchMultiplier
	}
	won := func(outPerSecond float64) int {
		output := math.Floor(seconds * outPerSecond)
		usd := (audio*priceAudioIn + text*priceTextIn + output*priceOutput) / 1e6 * factor
		return int(math.Round(usd * usdToKRW))
	}
	return won(outputLowPerSecond), won(outputHighPerSecond)
}

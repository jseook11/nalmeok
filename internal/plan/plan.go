// Package plan groups prepared audio pieces into requests and estimates cost.
// Everything here is a pure function (spec §5-3).
package plan

import (
	"math"

	"github.com/jseook11/nalmeok/internal/media"
)

// UnitMaxSeconds is the longest audio one request may carry (65 minutes).
// The answer and the model's thinking share one 65,536-token output cap, and
// a 110-minute lecture in one request ran past it (2026-11-05), so a request
// stays near an hour.
const UnitMaxSeconds = 65 * 60

// unitSlack is how far past maxSeconds a unit may run. EvenSeconds pads each
// piece by a second so frame rounding never leaves a few seconds over; the
// slack lets Units fold such a remainder into the last unit instead of
// sending it as a request of its own.
const unitSlack = 60

// Pricing for gemini-3.8-flash, USD per 1M tokens (the old Python app's MODELS).
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
//
// A unit may run up to unitSlack past maxSeconds.
func Units(tracks [][]media.Piece, maxSeconds float64) []UnitPlan {
	var units []UnitPlan
	for _, pieces := range tracks {
		first := len(units) // first unit of this file
		for _, p := range pieces {
			n := len(units)
			if n == first || units[n-1].Seconds+p.Seconds > maxSeconds+unitSlack {
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

// EvenSeconds is the piece length media.Prepare should cut seconds of audio
// at: the fewest requests of at most maxSeconds, all about the same length.
// 110 minutes at 65 becomes two pieces of 55, not 65 and 45. Audio that fits
// in one request is not cut (maxSeconds is returned).
func EvenSeconds(seconds, maxSeconds float64) float64 {
	if maxSeconds <= 0 || seconds <= maxSeconds {
		return maxSeconds
	}
	n := math.Ceil(seconds / maxSeconds)
	return seconds/n + 1
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

// EstimateMidKRW is the middle of EstimateKRW's range for Batch, the one
// number the window shows as "약 N원".
func EstimateMidKRW(seconds float64) int {
	low, high := EstimateKRW(seconds, true)
	return int(math.Round(float64(low+high) / 2))
}

// CostUSD prices reported tokens with the same table as EstimateKRW: audio
// and other prompt tokens at their input rates, answer and thinking tokens
// at the output rate, half price on Batch.
func CostUSD(audio, text, output int, batch bool) float64 {
	usd := (float64(audio)*priceAudioIn + float64(text)*priceTextIn + float64(output)*priceOutput) / 1e6
	if batch {
		usd *= batchMultiplier
	}
	return usd
}

// USDToKRW converts at the fixed rate EstimateKRW uses, rounded to won.
func USDToKRW(usd float64) int {
	return int(math.Round(usd * usdToKRW))
}

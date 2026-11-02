package plan

import "testing"

func TestCostMatchesEstimateRates(t *testing.T) {
	// One hour at the estimate's token rates, Batch: the same arithmetic as
	// EstimateKRW's high end.
	audio := 3600 * AudioTokensPerSecond
	got := USDToKRW(CostUSD(audio, promptTextTokens, 3600*3, true))
	if _, high := EstimateKRW(3600, true); got != high {
		t.Fatalf("cost = %d원, want the estimate's high end %d원", got, high)
	}
	if live := CostUSD(audio, 0, 0, false); live != 2*CostUSD(audio, 0, 0, true) {
		t.Fatalf("Batch must be half price: %v", live)
	}
	if mid := EstimateMidKRW(3600); mid < 70 || mid > 80 {
		t.Fatalf("EstimateMidKRW(1h) = %d, want about 74 (help text)", mid)
	}
}

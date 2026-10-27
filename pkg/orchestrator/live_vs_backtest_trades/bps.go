package live_vs_backtest_trades

// SlippageBps is (actual-model)/model in basis points. For a buy, positive
// means the live fill paid more than the model. For a sell, positive means
// the live fill sold higher than the model. ok is false when either price
// is missing.
func SlippageBps(actual, model float64) (bps float64, ok bool) {
	if actual <= 0 || model <= 0 {
		return 0, false
	}
	return (actual - model) / model * 10000, true
}

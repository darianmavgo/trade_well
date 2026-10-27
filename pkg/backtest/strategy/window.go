package strategy

import (
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// WindowFor returns the holding days and the absolute take-profit and stop-loss prices for a
// signal, from the strategy that produced it. Prices already on the signal win, and so does a
// per-signal HoldDaysOverride (short vs long legs). A nil strat leaves only what the signal
// carries. The caller passes the strategy in; WindowFor never looks one up in the registry.
func WindowFor(strat Strategy, sig models.Signal) (holdDays int, takeProfit, stopLoss float64) {
	takeProfit = sig.TakeProfit
	stopLoss = sig.StopLoss
	if strat != nil {
		cfg := strat.DefaultConfig()
		holdDays = cfg.HoldingWindow
		short := strings.EqualFold(sig.Direction, "SHORT")
		if short && cfg.ShortHoldingWindow > 0 {
			holdDays = cfg.ShortHoldingWindow
		}
		if takeProfit <= 0 && sig.BuyLimit > 0 {
			pct := cfg.TakeProfitPct
			if short && cfg.ShortTakeProfitPct > 0 {
				pct = cfg.ShortTakeProfitPct
			}
			if pct > 0 {
				takeProfit = sig.BuyLimit * (1 + pct)
			} else if cfg.TargetPct > 1 {
				takeProfit = sig.BuyLimit * cfg.TargetPct
			}
		}
		if stopLoss <= 0 && sig.BuyLimit > 0 && cfg.StopLossPct > 0 && cfg.StopLossPct < 1 {
			stopLoss = sig.BuyLimit * cfg.StopLossPct
		}
	}
	if sig.HoldDaysOverride > 0 {
		holdDays = sig.HoldDaysOverride
	}
	return holdDays, takeProfit, stopLoss
}

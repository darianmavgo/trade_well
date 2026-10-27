package manage_exits

import (
	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// windowForTrade is the manage_exits view: prefer values stored on the
// position at create time (already from the strategy / signal), then fall
// back to the strategy's DefaultConfig (via stratByID) if a row is missing
// them. stratByID is built by the caller from its resolved []strategy.Strategy
// -- manage_exits never calls strategy.Get itself.
func windowForTrade(trade Trade, stratByID map[string]strategy.Strategy) (holdDays int, target float64) {
	holdDays = trade.MaxTradingDays
	target = trade.TargetProfitPrice
	if holdDays > 0 && target > 0 {
		return holdDays, target
	}
	sig := backtestmodels.Signal{
		Symbol:    trade.Symbol,
		BuyLimit:  trade.EntryPrice,
		Direction: "LONG",
	}
	h, tp, _ := strategy.WindowFor(stratByID[trade.StrategyName], sig)
	if holdDays <= 0 {
		holdDays = h
	}
	if target <= 0 {
		target = tp
	}
	return holdDays, target
}

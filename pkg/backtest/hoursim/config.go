package hoursim

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sim"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// CrisisStop is the stop distance given to a strategy with none: the live bracket order carries
// a stop at 80% of the entry, so a backtest of a stop-less strategy sees it too. It must equal
// 1 - schwaber's trader.CrisisStopFraction.
const CrisisStop = 0.20

// UnsupportedError says which setting the hourly simulator does not model. A caller that must
// still run such a strategy decides what to do (fall back and label the result, or stop).
type UnsupportedError struct{ Feature string }

func (e UnsupportedError) Error() string {
	return "the hourly simulator does not model " + e.Feature
}

// Supported returns an UnsupportedError for the first setting in cfg that the hourly
// simulator ignores, or nil when it models all of them.
func Supported(cfg strategy.StrategyConfig) error {
	switch {
	case cfg.UseMargin:
		return UnsupportedError{"margin (UseMargin)"}
	case cfg.MarginInterestAnnual > 0:
		return UnsupportedError{"margin interest"}
	case cfg.UseATRStop:
		return UnsupportedError{"ATR stops"}
	case cfg.ExitAtMarketOpen:
		return UnsupportedError{"exit at market open"}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.PositionSizing)) {
	case "", "fixed_pct", "fixed_shares", "fixed_dollar":
	default:
		return UnsupportedError{fmt.Sprintf("position sizing %q", cfg.PositionSizing)}
	}
	return nil
}

// Config maps a strategy's execution settings and starting capital to sim's.
func Config(cfg strategy.StrategyConfig, capital float64) sim.Config {
	c := sim.Config{
		InitialCapital:      capital,
		Allocation:          cfg.AllocationPct,
		FlatAlloc:           cfg.FlatAllocation,
		CashYield:           cfg.CashYieldAnnual,
		Slippage:            cfg.SlippagePct,
		Commission:          cfg.CommissionPerShare,
		TakeProfitPct:       cfg.TakeProfitPct,
		TargetPct:           cfg.TargetPct,
		StopLossPct:         cfg.StopLossPct,
		HoldingWindow:       cfg.HoldingWindow,
		PositionCap:         cfg.PositionCap,
		ReentryCooldownDays: cfg.ReentryCooldownDays,
		OneFillPerMonth:     cfg.OneFillPerMonth,
		ShortBorrowAnnual:   cfg.ShortBorrowAnnual,
		CrisisStop:          CrisisStop,
		MaxBarVolumeShare:   cfg.MaxBarVolumeShare,
	}
	switch strings.ToLower(strings.TrimSpace(cfg.PositionSizing)) {
	case "fixed_shares":
		c.FixedShares = cfg.FixedShares
	case "fixed_dollar":
		c.FixedDollar = cfg.FixedDollar
	}
	if cfg.HoldingWindow >= strategy.NoHoldLimit {
		c.HoldingWindow = sim.NoHoldLimit
	}
	if cfg.UseTrailingStop {
		c.TrailingStop = cfg.TrailingStopPct
	}
	c.EntryGTC = cfg.EntryGTC()
	switch {
	case cfg.EntryLimitPct > 0:
		// The signal is dated on the entry session itself and the limit hangs off the previous close.
		c.EntryLimitPct, c.EntrySameDay = cfg.EntryLimitPct, true
	case cfg.NextDayOpenEntry:
		c.NextDayOpenEntry = true
	case cfg.NextDayLimitEntry:
		c.NextDayLimitEntry = true
	}
	return c
}

// Signals maps signals to sim's. Dates are cut to the day.
func Signals(in []models.Signal) []sim.Signal {
	out := make([]sim.Signal, 0, len(in))
	for _, s := range in {
		out = append(out, sim.Signal{
			Symbol:     s.Symbol,
			Date:       day(s.Date),
			Entry:      min(s.Entry, 1),
			Short:      strings.EqualFold(s.Direction, "SHORT"),
			Close:      s.Close,
			BuyLimit:   s.BuyLimit,
			TakeProfit: s.TakeProfit,
			StopLoss:   s.StopLoss,
			Alloc:      s.AllocationPctOverride,
			HoldDays:   s.HoldDaysOverride,
		})
	}
	return out
}

func day(d string) string {
	if len(d) > 10 {
		return d[:10]
	}
	return d
}

var exitReasons = map[string]models.ExitReason{
	"stop_loss":     models.ExitReasonStopLoss,
	"profit_target": models.ExitReasonProfitTarget,
	"trailing_stop": models.ExitReasonTrailingStop,
	"time_up":       models.ExitReasonTimeUp,
	"signal":        models.ExitReasonSignal,
	"preempted":     models.ExitReasonPreempted,
	"end":           models.ExitReasonEndBacktest,
}

// Trade maps one finished sim trade to the trade type reports and storage use.
func Trade(id int, t sim.Trade, commissionPerShare float64) models.Trade {
	reason, ok := exitReasons[t.Reason]
	if !ok {
		reason = models.ExitReason(strings.ToUpper(t.Reason))
	}
	invested := float64(t.Shares) * t.EntryPrice
	return models.Trade{
		ID:              id,
		StrategyID:      t.Strategy,
		Symbol:          t.Symbol,
		EntryDate:       day(t.Entry),
		EntryPrice:      t.EntryPrice,
		TargetPrice:     t.Target,
		StopLossPrice:   t.Stop,
		ExitDate:        day(t.Exit),
		ExitPrice:       t.ExitPrice,
		ExitReason:      reason,
		Shares:          t.Shares,
		InvestedCapital: invested,
		GrossPnL:        t.NetPnL,
		NetPnL:          t.NetPnL,
		ReturnPct:       t.ReturnPct,
		HoldDays:        t.HoldDays,
		CommissionPaid:  2 * float64(t.Shares) * commissionPerShare,
	}
}

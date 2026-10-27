package gridsearch

import (
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// resolveParamSpace applies the same signal/symbol/alloc/yield overrides used
// by prepareSweep, without touching the database — AssessParameterSpace only
// looks at the strategy's own Go-side defaults/RequiredSymbols, so this is
// safe to call with no DB connection.
func resolveParamSpace(strat strategy.Strategy, opts sweepOptions) strategy.ParameterSpace {
	space := strategy.AssessParameterSpace(strat)
	if opts.AllocOverride != nil {
		space.Allocations = []float64{*opts.AllocOverride}
	}
	if opts.CashYieldOverride != nil {
		space.CashYield = *opts.CashYieldOverride
	}
	if len(opts.SymbolOverride) > 0 {
		space.Symbols = opts.SymbolOverride
	}
	if opts.SignalOverride != "" {
		space.SignalSymbol = opts.SignalOverride
	}
	return space
}

// declineRelevance reports whether the consecutive decline/rally-day count is
// actually being searched for this strategy, and a one-line explanation.
// Every "drop"/"rally" strategy's entry signal is defined by a multiday
// decline (or rally) streak in the signal symbol — buildSignals in
// main.go varies that streak length per space.SignalDays — so if that list
// ever collapsed to a single value the sweep would silently stop searching
// the one parameter that defines the entry itself, and only optimize
// TP/SL/hold around an arbitrary fixed streak length. tree_bounce strategies
// are the deliberate exception: their entries come from a fitted decision
// tree, not a streak count, so a fixed SignalDays there is correct.
func declineRelevance(space strategy.ParameterSpace) (relevant bool, note string) {
	if space.EntriesFixed() {
		return true, fmt.Sprintf("n/a — decision-tree entries, not a decline/rally streak (fixed at %v)", space.SignalDays)
	}
	if len(space.SignalDays) <= 1 {
		return false, fmt.Sprintf("⚠️  NOT being searched — only %v is evaluated; widen ParameterSpace.SignalDays", space.SignalDays)
	}
	return true, fmt.Sprintf("✅ searched — %d values %v (baseline %dd)", len(space.SignalDays), space.SignalDays, space.Baseline.SignalDays)
}

// printParamsCommand implements `gridsearch params <strategy|all>` — prints
// the resolved parameter grid for each target strategy and exits. No DB
// connection is opened and no backtests are run.
func printParamsCommand(targets []strategy.Strategy, opts sweepOptions) {
	fmt.Printf("\n=======================================================================================================================\n")
	fmt.Printf("📋 GRID SEARCH PARAMETER SPACE (%d strategy/strategies — no backtests run)\n", len(targets))
	fmt.Printf("=======================================================================================================================\n")

	var flagged []string
	for _, strat := range targets {
		space := resolveParamSpace(strat, opts)
		if len(opts.PeriodOverride) > 0 && len(space.Periods) > 0 {
			space.Periods = opts.PeriodOverride
		}
		totalPerms := space.Perms()
		ok, note := declineRelevance(space)
		if !ok {
			flagged = append(flagged, strat.ID())
		}

		fmt.Printf("\n• %-20s %s\n", strat.ID(), strat.Name())
		fmt.Printf("    Signal Symbol / Tradable:  %s / %v   Direction: %s\n", space.SignalSymbol, space.Symbols, space.Direction)
		fmt.Printf("    Decline/Rally Days:        %s\n", note)
		if len(space.Periods) > 0 {
			fmt.Printf("    Periods:                   %v\n", space.Periods)
		}
		if len(space.EntryLimits) > 0 {
			fmt.Printf("    Buy Limit (of prev close): %v\n", formatPercents(space.EntryLimits))
		}
		fmt.Printf("    Holding Windows:           %v days\n", space.HoldDays)
		fmt.Printf("    Take-Profit:               %v\n", formatPercents(space.TakeProfits))
		fmt.Printf("    Stop-Loss:                 %v\n", formatPercents(space.StopLosses))
		fmt.Printf("    Regime Filters:            %v\n", space.Regimes)
		fmt.Printf("    Allocations:               %v\n", formatPercents(space.Allocations))
		fmt.Printf("    Total permutations:        %d\n", totalPerms)
	}

	fmt.Printf("\n=======================================================================================================================\n")
	if len(flagged) == 0 {
		fmt.Printf("✅ Every non-tree_bounce strategy above is actually sweeping its decline/rally-day count.\n")
	} else {
		fmt.Printf("⚠️  %d strategy/strategies are NOT sweeping their decline/rally-day count: %v\n", len(flagged), flagged)
	}
	fmt.Printf("=======================================================================================================================\n\n")
}

package strateval

import "fmt"

// Gates are hard thresholds for tier-A eligibility (OOS-focused).
// Tuned for stacked books: prefer high win rate + contained drawdown over
// high trade counts (monthly overlays may only fire ~once per month).
type Gates struct {
	MinOOSTrades      int     // soft floor for statistical sanity (default 12)
	MinOOSSharpe      float64 // default 0 → must be strictly >
	MinOOSAvgTradePct float64 // default 0 → must be strictly >
	MinOOSWinRate     float64 // fraction 0–1 (default 0.55)
	MaxOOSDDAbs       float64 // absolute OOS max DD cap (default 0.15 = 15%)
	MaxOOSDDMultiple  float64 // OOS DD may be at most this × IS DD (default 1.5)
}

// DefaultGates returns stack-friendly promote-loop defaults.
func DefaultGates() Gates {
	return Gates{
		MinOOSTrades:      12,
		MinOOSSharpe:      0,
		MinOOSAvgTradePct: 0,
		MinOOSWinRate:     0.55,
		MaxOOSDDAbs:       0.15,
		MaxOOSDDMultiple:  1.5,
	}
}

// Metrics is the slice of a PerformanceReport we persist and gate on.
type Metrics struct {
	StartDate      string
	EndDate        string
	CAGR           float64
	Sharpe         float64
	MaxDD          float64
	Trades         int
	WinRate        float64
	AvgTradePct    float64
	TotalReturnPct float64
	// IdleDays of Days sessions had no open position. Days 0 means not recorded.
	IdleDays int
	Days     int
}

// AssignTier classifies a strategy. inAllowlist marks decaying live names as D
// when they fail gates (otherwise they would just be C).
func AssignTier(is, oos Metrics, g Gates, inAllowlist bool) (tier string, reasons []string) {
	var fails []string
	if oos.Trades < g.MinOOSTrades {
		fails = append(fails, fmt.Sprintf("oos_trades=%d < %d", oos.Trades, g.MinOOSTrades))
	}
	if oos.WinRate < g.MinOOSWinRate {
		fails = append(fails, fmt.Sprintf("oos_win_rate=%.1f%% < %.1f%%", oos.WinRate*100, g.MinOOSWinRate*100))
	}
	if g.MaxOOSDDAbs > 0 && oos.MaxDD > g.MaxOOSDDAbs {
		fails = append(fails, fmt.Sprintf("oos_dd=%.1f%% > abs_cap %.1f%%", oos.MaxDD*100, g.MaxOOSDDAbs*100))
	}
	if is.MaxDD > 0 && g.MaxOOSDDMultiple > 0 && oos.MaxDD > is.MaxDD*g.MaxOOSDDMultiple {
		fails = append(fails, fmt.Sprintf("oos_dd=%.1f%% > %.1fx is_dd=%.1f%%", oos.MaxDD*100, g.MaxOOSDDMultiple, is.MaxDD*100))
	}
	if oos.Sharpe <= g.MinOOSSharpe {
		fails = append(fails, fmt.Sprintf("oos_sharpe=%.2f <= %.2f", oos.Sharpe, g.MinOOSSharpe))
	}
	if oos.AvgTradePct <= g.MinOOSAvgTradePct {
		fails = append(fails, fmt.Sprintf("oos_avg_trade=%.4f <= %.4f", oos.AvgTradePct, g.MinOOSAvgTradePct))
	}
	if len(fails) == 0 {
		return "A", []string{"passes OOS hard gates (win-rate + DD focused)"}
	}
	// Marginal: stacking-friendly quality signals even if a hard gate missed
	softTrades := g.MinOOSTrades / 2
	if softTrades < 6 {
		softTrades = 6
	}
	if oos.Sharpe > 0 && oos.WinRate >= g.MinOOSWinRate*0.9 && oos.Trades >= softTrades {
		if inAllowlist {
			return "B", append([]string{"marginal but on allowlist"}, fails...)
		}
		return "B", append([]string{"marginal"}, fails...)
	}
	if inAllowlist {
		return "D", append([]string{"on allowlist but failing OOS gates — demote candidate"}, fails...)
	}
	return "C", append([]string{"research only"}, fails...)
}

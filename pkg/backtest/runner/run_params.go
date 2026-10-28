package runner

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// RunSettings is what a report states up front about the run that made it.
type RunSettings struct {
	Command      string // the run mode, e.g. "backtest" or "backtest stack"
	MarketDB     string
	Table        string
	Symbols      string // -symbol filter or symbol list; "" = each strategy's own symbols
	Start, End   string
	HoldoutMonth int
	Capital      float64
	DefaultAsset string
	Override     ConfigOverride
}

// DescribeRun builds the parameter groups for a report: the run itself, then
// the effective exit and sizing settings of each strategy (defaults with the
// CLI override applied, which is what the simulator ran).
func DescribeRun(rs RunSettings, strats []strategy.Strategy) []models.ParamGroup {
	run := &models.ParamGroup{Title: "Run"}
	if rs.Command != "" {
		run.Add("Command", rs.Command)
	}
	srcs := MarketSources(rs.MarketDB)
	run.Add("Market DB", srcs[0].Path+" (table "+rs.Table+"; "+srcs[0].Detail+"; "+srcs[0].Use+")")
	run.Add("Symbols", orDefault(rs.Symbols, "each strategy's own symbols"))
	run.Add("Window", orDefault(rs.Start, "first bar")+" to "+orDefault(rs.End, "last bar"))
	if rs.HoldoutMonth > 0 {
		run.Add("Out-of-sample holdout", fmt.Sprintf("last %d months", rs.HoldoutMonth))
	}
	run.Add("Starting capital", fmt.Sprintf("$%.2f", rs.Capital))
	if rs.DefaultAsset != "" {
		run.Add("Park symbol (residual cash)", strings.ToUpper(rs.DefaultAsset))
	}
	o := rs.Override
	if o.StopLoss > 0 || o.Target > 0 || o.Hold > 0 || o.MaxPositions > 0 || o.AllocPct > 0 {
		run.Add("CLI overrides", fmt.Sprintf("stoploss=%v target=%v hold=%d max-positions=%d alloc=%v",
			o.StopLoss, o.Target, o.Hold, o.MaxPositions, o.AllocPct))
	} else {
		run.Add("CLI overrides", "none")
	}
	groups := []models.ParamGroup{*run}

	const maxListed = 12
	for i, s := range strats {
		if i == maxListed {
			groups = append(groups, models.ParamGroup{Title: fmt.Sprintf("… %d more strategies not listed", len(strats)-maxListed)})
			break
		}
		cfg := rs.Override.Apply(s.DefaultConfig())
		g := &models.ParamGroup{Title: "Strategy " + s.ID()}
		if req, ok := s.(strategy.RequiredSymbolsProvider); ok && len(req.RequiredSymbols()) > 0 {
			g.Add("Symbols", strings.Join(req.RequiredSymbols(), ", "))
		}
		g.Add("Take profit", describeTakeProfit(cfg))
		g.Add("Stop loss", describeStop(cfg))
		g.Add("Holding window", fmt.Sprintf("%d days", cfg.HoldingWindow))
		g.Add("Position cap", fmt.Sprintf("%d", cfg.PositionCap))
		g.Add("Allocation", fmt.Sprintf("%.1f%% of equity (%s)", cfg.AllocationPct*100, orDefault(cfg.PositionSizing, "fixed_pct")))
		g.Add("Slippage / commission", fmt.Sprintf("%.4f%% / $%.4f per share", cfg.SlippagePct*100, cfg.CommissionPerShare))
		g.Add("Entry model", describeEntry(cfg))
		if cfg.SameDayExit {
			g.Add("Same-day exit", "on")
		}
		groups = append(groups, *g)
	}
	return groups
}

func describeTakeProfit(cfg strategy.StrategyConfig) string {
	switch {
	case cfg.TakeProfitPct > 0:
		return fmt.Sprintf("+%.2f%%", cfg.TakeProfitPct*100)
	case cfg.TargetPct > 1:
		return fmt.Sprintf("+%.2f%% (target %.4f)", (cfg.TargetPct-1)*100, cfg.TargetPct)
	}
	return "none"
}

func describeStop(cfg strategy.StrategyConfig) string {
	var parts []string
	switch {
	case cfg.StopLossPct > 0 && cfg.StopLossPct < 1:
		parts = append(parts, fmt.Sprintf("-%.2f%% (multiplier %.4f)", (1-cfg.StopLossPct)*100, cfg.StopLossPct))
	case cfg.StopLossPct >= 1:
		parts = append(parts, fmt.Sprintf("%.4f", cfg.StopLossPct))
	}
	if cfg.UseATRStop {
		parts = append(parts, fmt.Sprintf("ATR stop x%.2f", cfg.ATRStopMultiplier))
	}
	if cfg.UseTrailingStop {
		parts = append(parts, fmt.Sprintf("trailing %.2f%%", cfg.TrailingStopPct*100))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func describeEntry(cfg strategy.StrategyConfig) string {
	d := describeEntryOrder(cfg)
	if cfg.EntryGTC() && (cfg.NextDayLimitEntry || cfg.EntryLimitPct > 0) {
		d += ", good till cancelled"
	}
	return d
}

func describeEntryOrder(cfg strategy.StrategyConfig) string {
	switch {
	case cfg.NextDayOpenEntry:
		return "next-day market open"
	case cfg.NextDayLimitEntry:
		return "next-day limit at signal price"
	case cfg.EntryLimitPct > 0:
		return fmt.Sprintf("same-session limit at %.2f%% of prior close", cfg.EntryLimitPct*100)
	}
	return "signal-bar close"
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

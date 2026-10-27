package backtest

import (
	"fmt"
	"log"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
)

// optimizedParams is one strategy's best (highest resilience score) row from
// reports/gridsearch.db's gridsearch_results, read back as real values
// instead of parsing the display Label.
type optimizedParams struct {
	Label      string  `db:"label"`
	SignalDays int     `db:"signal_days"`
	HoldDays   int     `db:"hold_days"`
	TakeProfit float64 `db:"take_profit_pct"` // fractional offset, e.g. 0.05 for +5%
	StopLoss   float64 `db:"stop_loss_pct"`   // fractional offset, e.g. 0.05 for -5%
	Regime     string  `db:"regime"`
	Score      float64 `db:"resilience_score"`
}

// bestParamsFor reads strategy_id's best-by-resilience-score row from
// gridsearch_results. ok is false if the strategy has never been swept, or
// its only recorded row(s) predate the symbol/signal_days/hold_days/
// take_profit_pct/stop_loss_pct/regime columns (added after this table
// shipped — see ensureGridSearchSchema) and so carry no usable params: every
// legitimate winning config has a non-zero hold_days (the swept grid never
// includes 0), so hold_days == 0 reliably means "old row, re-sweep this
// strategy to benefit from `optimized`" rather than a real zero-day hold.
func bestParamsFor(gdb *sqlx.DB, strategyID string) (optimizedParams, bool) {
	var p optimizedParams
	err := gdb.Get(&p, `
		SELECT label,
		       COALESCE(signal_days, 0) AS signal_days,
		       COALESCE(hold_days, 0) AS hold_days,
		       COALESCE(take_profit_pct, 0) AS take_profit_pct,
		       COALESCE(stop_loss_pct, 0) AS stop_loss_pct,
		       COALESCE(regime, '') AS regime,
		       resilience_score
		FROM gridsearch_results
		WHERE strategy_id = ?
		ORDER BY resilience_score DESC
		LIMIT 1
	`, strategyID)
	if err != nil || p.HoldDays == 0 {
		return optimizedParams{}, false
	}
	return p, true
}

// runOptimizedCommand implements `gridsearch apply`: run every selected
// strategy with the best (highest resilience score) config a prior
// `gridsearch` sweep found for it, instead of the strategy's own hardcoded
// baseline. Strategies with no sweep on record run with their baseline
// DefaultConfig() instead (there's no "optimized" params to apply) and are
// called out explicitly, since a silent fallback would look like every
// strategy got optimized when some didn't.
func runOptimizedCommand(stratArg, targetDb, tableName, outDir, gridDBPath string, capital float64, symbolFilter string, autoDownload bool, downloadYears, concurrency int, reinvestDividends bool, allocPct float64) error {
	targets, err := runner.ResolveStrategies(stratArg, "all")
	if err != nil {
		return fmt.Errorf("%v. Run with -strategylist to see available strategies.", err)
	}

	gdb, err := storage.OpenSQLite(gridDBPath)
	if err != nil {
		return fmt.Errorf("Failed to open gridsearch DB %s: %v", gridDBPath, err)
	}
	// Look up every target's best config up front, before opening the market
	// DB or starting the worker pool — sqlite3 handles concurrent readers
	// fine, but there's no reason to hold gdb open across a long backtest run.
	paramsByID := make(map[string]optimizedParams, len(targets))
	var noSweep, regimeCaveat []string
	for _, s := range targets {
		p, ok := bestParamsFor(gdb, s.ID())
		if !ok {
			noSweep = append(noSweep, s.ID())
			continue
		}
		paramsByID[s.ID()] = p
		if p.Regime != "" && p.Regime != "All Regimes" {
			regimeCaveat = append(regimeCaveat, fmt.Sprintf("%s (winning config used regime %q — gridsearch's regime filter is a proxy-model-only concept with no equivalent in the real strategy's signal logic, so it can't be applied; running with the rest of the winning config: %s)",
				s.ID(), p.Regime, p.Label))
		}
	}
	gdb.Close()

	fmt.Printf("\n=======================================================================================================================\n")
	fmt.Printf("🏆 OPTIMIZED BACKTEST — applying each strategy's best gridsearch config (by resilience score)\n")
	fmt.Printf("=======================================================================================================================\n")
	fmt.Printf("   %d/%d strategies have a gridsearch sweep to apply.\n", len(paramsByID), len(targets))
	if len(noSweep) > 0 {
		fmt.Printf("\n⚠️  %d strategies have never been swept — running with their baseline defaults instead (run `gridsearch -strategy <id>` first to optimize them):\n", len(noSweep))
		runner.PrintMissingList(noSweep)
	}
	if len(regimeCaveat) > 0 {
		fmt.Printf("\n⚠️  %d strategies' winning config can only be partially applied:\n", len(regimeCaveat))
		for _, c := range regimeCaveat {
			fmt.Printf("   - %s\n", c)
		}
	}
	fmt.Println()

	if err := runner.DetectAndDownloadMissingData(targetDb, tableName, targets, symbolFilter, autoDownload, downloadYears); err != nil {
		return fmt.Errorf("Market data resolution error: %v", err)
	}

	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return fmt.Errorf("Failed to open source DB %s: %v", targetDb, err)
	}
	defer db.Close()

	workers := concurrency
	if workers < 1 {
		workers = 1
	}
	fmt.Printf("⚙️  Concurrency: %d workers, bars loaded per batch of up to %d strategies (Starting Capital: $%.2f)\n\n", workers, runner.DefaultBatchSize, capital)

	results, err := runner.RunBatched(runner.BatchOptions{
		DB: db, Table: tableName, Start: backtestStart,
		SymbolFilter: symbolFilter, Workers: workers,
	}, targets, func(s strategy.Strategy, barsBySymbol map[string][]models.Bar, sortedDates []string) runner.RunResult {
		var cfg strategy.StrategyConfig
		if p, ok := paramsByID[s.ID()]; ok {
			targetMult := 0.0
			if p.TakeProfit > 0 {
				targetMult = 1.0 + p.TakeProfit
			}
			stopMult := 0.0
			if p.StopLoss > 0 {
				stopMult = 1.0 - p.StopLoss
			}
			if dc, ok := s.(strategy.DeclineDaysConfigurable); ok && p.SignalDays > 0 {
				dc.SetDeclineDays(p.SignalDays)
			}
			cfg = runner.BuildConfig(s, stopMult, targetMult, p.HoldDays, 0)
		} else {
			cfg = s.DefaultConfig()
		}
		cfg = runner.ConfigOverride{AllocPct: allocPct}.Apply(cfg)

		res := runner.ExecuteStrategyWithDividends(s, cfg, barsBySymbol, sortedDates, capital, symbolFilter, outDir, targetDb, reinvestDividends)
		if res.Err != nil {
			log.Printf("❌ [%s] Error: %v\n", s.ID(), res.Err)
		} else {
			tag := "baseline"
			if _, ok := paramsByID[s.ID()]; ok {
				tag = "optimized"
			}
			fmt.Printf("✅ [%s/%s] Completed: %d signals, %d trades, Return: %+.2f%%, Sharpe: %.2f ➔ %s\n",
				s.ID(), tag, res.SignalCount, len(res.Trades), res.Report.TotalReturnPct*100, res.Report.SharpeRatio, res.DbPath)
		}
		return res.Slim()
	})
	if err != nil {
		return fmt.Errorf("Error loading historical bars for simulation: %v", err)
	}

	runner.PrintComparisonTable(results)

	return nil
}

package backtest

import (
	"fmt"
	"log"
	"os"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// runStaleCommand implements `backtest stale`: assess every strategy with a
// usable result in outDir for staleness (see runner.AssessOne) and print a
// report. No backtests are run.
func runStaleCommand(outDir, marketDBPath string, concurrency int) error {
	fmt.Println("🔎 Checking", outDir, "for strategies with results to assess...")
	byStrategy, _, _, _, _ := runner.ScanAndValidate(outDir, concurrency)
	if len(byStrategy) == 0 {
		fmt.Println("No usable results found — nothing to assess. Run some backtests first.")
		return nil
	}

	marketDB, err := storage.OpenSQLite(marketDBPath)
	if err != nil {
		log.Printf("Warning: could not open market DB %s (%v) — data-freshness checks will be skipped", marketDBPath, err)
		marketDB = nil
	} else {
		defer marketDB.Close()
	}

	var entries []runner.StaleEntry
	for id, result := range byStrategy {
		computed := result.ComputedAt
		if computed.IsZero() {
			info, err := os.Stat(result.DbPath)
			if err != nil {
				continue
			}
			computed = info.ModTime()
		}
		entries = append(entries, runner.AssessOne(id, computed, result.Report.EndDate, marketDB))
	}

	runner.PrintStalenessReport(outDir, entries, runner.MissingStrategies(byStrategy))

	return nil
}

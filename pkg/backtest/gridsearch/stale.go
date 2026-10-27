package gridsearch

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// runStaleCommand implements `gridsearch stale`: assess every strategy with a
// completed sweep in gridDBPath for staleness (see runner.AssessOne) and
// print a report. No sweeps are run.
func runStaleCommand(gridDBPath, marketDBPath, strategyArg string) error {
	gdb, err := storage.OpenSQLite(gridDBPath)
	if err != nil {
		return fmt.Errorf("Failed to open gridsearch pipeline DB %s: %v", gridDBPath, err)
	}
	defer gdb.Close()
	if err := ensureGridSearchSchema(gdb); err != nil {
		return fmt.Errorf("Failed to initialize gridsearch pipeline schema: %v", err)
	}

	type row struct {
		StrategyID  string `db:"strategy_id"`
		FinishedAt  string `db:"finished_at"`
		DataMaxDate string `db:"data_max_date"`
	}
	var rows []row
	if err := gdb.Select(&rows, `SELECT strategy_id, finished_at, COALESCE(data_max_date, '') AS data_max_date FROM gridsearch_runs WHERE status = 'done'`); err != nil {
		return fmt.Errorf("Failed to query gridsearch_runs: %v", err)
	}
	if keep := idSet(strategyArg); keep != nil {
		kept := rows[:0]
		for _, r := range rows {
			if keep[strings.ToLower(r.StrategyID)] {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if len(rows) == 0 {
		log.Println("No completed sweeps found in", gridDBPath, "— nothing to assess. Run some sweeps first.")
		return nil
	}

	marketDB, err := storage.OpenSQLite(marketDBPath)
	if err != nil {
		log.Printf("Warning: could not open market DB %s (%v) — data-freshness checks will be skipped", marketDBPath, err)
		marketDB = nil
	} else {
		defer marketDB.Close()
	}

	swept := make(map[string]bool, len(rows))
	var entries []runner.StaleEntry
	for _, r := range rows {
		swept[r.StrategyID] = true
		computedAt, err := time.Parse(time.RFC3339, r.FinishedAt)
		if err != nil {
			continue
		}
		entries = append(entries, runner.AssessOne(r.StrategyID, computedAt, r.DataMaxDate, marketDB))
	}

	keep := idSet(strategyArg)
	var neverSwept []string
	for _, s := range strategy.ListAll() {
		if !swept[s.ID()] && (keep == nil || keep[strings.ToLower(s.ID())]) {
			neverSwept = append(neverSwept, s.ID())
		}
	}
	sort.Strings(neverSwept)

	runner.PrintStalenessReport(gridDBPath, entries, neverSwept)

	return nil
}

// idSet is the lower-cased ids of a -strategy list, or nil (no filter) when the
// list is empty or "all".
func idSet(arg string) map[string]bool {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.EqualFold(arg, "all") {
		return nil
	}
	out := map[string]bool{}
	for _, id := range strings.Split(arg, ",") {
		out[strings.ToLower(strings.TrimSpace(id))] = true
	}
	return out
}

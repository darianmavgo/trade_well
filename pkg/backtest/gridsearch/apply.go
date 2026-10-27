package gridsearch

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/backtest"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// runApply is `gridsearch apply`: re-run the strategies named by -strategy
// (default: all) with the best config an earlier sweep found for each, in the
// run folder that holds the sweep. The backtest itself is pkg/backtest's.
func runApply(conf Config) error {
	defer storage.CloseSharedResults()
	bt := backtest.DefaultConfig()
	bt.Mode = "optimized"
	bt.Db = conf.Db
	bt.Strategy = strings.TrimSpace(conf.Strategy)
	if bt.Strategy == "" {
		bt.Strategy = strings.Join(conf.Args, ",")
	}
	bt.RunID = conf.RunID
	bt.GridsearchDb = conf.GridsearchDb
	bt.Capital = conf.Capital
	bt.Symbol = conf.Symbol
	bt.Concurrency = conf.Concurrency
	if conf.Passed["alloc"] {
		bt.Alloc = conf.Alloc
	}
	if err := backtest.Run(bt); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	return nil
}

// Package stratreg registers every strategy source (SQL pipelines plus the
// refdata-driven families) so any command resolves the same ids as backtest.
package stratreg

import (
	"github.com/darianmavgo/trade_well/pkg/backtest/hmm_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/markov_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/rotation_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/streak_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/tree_strategy"
)

// RegisterAll registers SQL pipelines found under root/sql/strategies (calc
// databases at db) and the refdata families. It mirrors the sequence in
// pkg/backtest.
func RegisterAll(root, db string) {
	strategy.AutoRegisterSQLStrategies(root, db)
	RegisterFamilies()
}

// RegisterFamiliesAt registers the row-backed families reading the reference
// database at path, not the default. An embedding program that sets its
// folder after start-up (trade_orchestrator) uses it to point at the file it
// placed. Like RegisterFamilies it reads no rows.
func RegisterFamiliesAt(path string) {
	streak_strategy.RegisterFrom(path)
	tree_strategy.RegisterFrom(path)
	hold_strategy.RegisterFrom(path)
	markov_strategy.RegisterFrom(path)
	hmm_strategy.RegisterFrom(path)
	rotation_strategy.RegisterFrom(path)
}

// RegisterFamilies registers the row-backed families (streak, tree, hold,
// markov, hmm, rotation). It reads no rows: a member is built when strategy.Get asks.
func RegisterFamilies() {
	streak_strategy.Register()
	tree_strategy.Register()
	hold_strategy.Register()
	markov_strategy.Register()
	hmm_strategy.Register()
	rotation_strategy.Register()
}

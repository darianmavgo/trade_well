package live_vs_backtest_trades

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// TableName is the decision ledger. One row per account, strategy, symbol
// and signal date. live_vs_backtest_trades rebuilds it; live_vs_backtest_slippage reads it.
const TableName = "live_vs_backtest_trade"

// EnsureSchema creates the ledger. It only adds.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("live_vs_backtest_trades/ensure_schema"))
	return err
}

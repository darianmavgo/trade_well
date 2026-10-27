package live_vs_backtest_slippage

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// TableName is one row per strategy: average entry and exit slippage, the
// worst entry slippage, the average sessions between the intended session and
// the entry fill, and fees.
const TableName = "live_vs_backtest_slippage"

// EnsureSchema creates the summary table.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("live_vs_backtest_slippage/ensure_schema"))
	return err
}

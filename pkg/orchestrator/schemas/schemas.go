// Package schemas is the one list of every package's EnsureSchema. The job
// bootstrap (pkg/bootstrap) and the dashboard both call EnsureAll, so a
// database always has every table from the moment it is opened, whichever job
// ran first, and a viewer never sees "no such table" for a job that simply has
// not run yet. Every EnsureSchema is idempotent and only adds.
package schemas

import (
	"database/sql"
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/auth"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_slippage"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/trade_history"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// EnsureAll creates every table and index that does not exist yet, reporting
// the first package that fails. On Turso it runs once per process (see dbutil.EnsureOnce).
func EnsureAll(db *sql.DB) error {
	return dbutil.EnsureOnce("schemas.EnsureAll", func() error { return ensureAll(db) })
}

func ensureAll(db *sql.DB) error {
	ensurers := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{"appconfig", appconfig.EnsureSchema},
		{"account_state", account_state.EnsureSchema},
		{"job_runs", job_runs.EnsureSchema},
		{"killswitch", killswitch.EnsureSchema},
		{"risk", risk.EnsureSchema},
		{"strategies", strategies.EnsureSchema},
		{"account_allowlist", account_allowlist.EnsureSchema},
		{"auth", auth.EnsureSchema},
		{"scan_market", scan_market.EnsureSchema},
		{"stage_orders", stage_orders.EnsureSchema},
		{"submit_orders", submit_orders.EnsureSchema},
		{"manage_exits", manage_exits.EnsureSchema},
		{"trade_history", trade_history.EnsureSchema},
		{"live_vs_backtest_trades", live_vs_backtest_trades.EnsureSchema},
		{"live_vs_backtest_slippage", live_vs_backtest_slippage.EnsureSchema},
		{"check_health", check_health.EnsureSchema},
		{"send_email", send_email.EnsureSchema},
		{"runtimeenv", runtimeenv.EnsureSchema},
	}
	for _, e := range ensurers {
		if err := e.fn(db); err != nil {
			return fmt.Errorf("ensure %s schema: %w", e.name, err)
		}
	}
	return nil
}

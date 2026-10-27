package live_vs_backtest_report

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
)

// writeReportTables writes one build's tables in Turso, renders the page from them and writes the
// HTML file. The tables are named for the run (see runtables); the newest few builds are kept.
// It returns the run id.
func writeReportTables(ctx context.Context, db *sql.DB, opts Options, notes []note, trips []roundTrip) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	run := runtables.Begin(db, reportTables)
	defer func() { _ = runtables.Prune(context.WithoutCancel(ctx), db, reportTables, keepRuns) }()
	if err := run.Exec(ctx, reportDDL); err != nil {
		return run.ID, err
	}
	meta := [][]any{
		{"built_at", time.Now().UTC().Format(time.RFC3339)},
		{"run_id", run.ID},
		{"allowlist", strings.Join(opts.Allowlist, ",")},
		{"account", strings.TrimSpace(opts.Account)},
		{"market_db", opts.MarketDB},
		{"market_table", opts.Table},
		{"html", opts.HTML},
	}
	if err := run.InsertRows(ctx, "live_vs_backtest_report_meta", []string{"key", "value"}, meta); err != nil {
		return run.ID, err
	}
	noteRows := make([][]any, len(notes))
	for i, n := range notes {
		noteRows[i] = []any{n.Severity, n.Message}
	}
	if err := run.InsertRows(ctx, "live_vs_backtest_report_note", []string{"severity", "message"}, noteRows); err != nil {
		return run.ID, err
	}
	tripRows := make([][]any, len(trips))
	for i, t := range trips {
		tripRows[i] = []any{t.account, t.strategy, t.symbol, t.greenlit, t.buyID, t.sellID,
			t.buyPx, t.sellPx, t.qty, t.fees, nullFloat(t.cash), nullFloat(t.ret),
			t.opened, t.closed, t.exitID, t.status}
	}
	if err := run.InsertRows(ctx, "live_vs_backtest_report_round_trip", []string{
		"account_number", "strategy_id", "symbol", "greenlit", "buy_order_id", "sell_order_id",
		"buy_price", "sell_price", "qty", "fees", "cash_pnl", "return_pct", "opened_at", "closed_at",
		"manage_exit_id", "manage_status"}, tripRows); err != nil {
		return run.ID, err
	}
	body, err := render(run, db)
	if err != nil {
		return run.ID, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.HTML), 0o755); err != nil && filepath.Dir(opts.HTML) != "." {
		return run.ID, err
	}
	return run.ID, os.WriteFile(opts.HTML, []byte(body), 0o644)
}

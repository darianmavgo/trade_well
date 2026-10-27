package live_vs_backtest_sync

// Reads backtest result ASSETS: the per-run result databases (<reports>/<run>/*.db and oos/*.db)
// that backtestgosqlite writes. They are opened read-only; nothing here writes one.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
	_ "modernc.org/sqlite"
)

// copyBacktestResultAssetSnapshot writes a consistent copy of the backtest result ASSET (a SQLite file built in
// backtestgosqlite) src at dst, through a temporary file so
// a reader never sees half of it.
func copyBacktestResultAssetSnapshot(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	os.Remove(tmp)
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	conn, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}).String())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `VACUUM INTO `+quote(tmp)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("live_vs_backtest_sync: copy %s: %w", src, err)
	}
	return os.Rename(tmp, dst)
}

// scanBacktestResultAssetPerformanceSummary reads one backtest result ASSET file's performance_summary into bs_found, for the wanted strategies.
func scanBacktestResultAssetPerformanceSummary(ctx context.Context, calc *runtables.Run, f resultFile, want map[string]bool) error {
	abs, err := filepath.Abs(f.path)
	if err != nil {
		return err
	}
	src, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}).String())
	if err != nil {
		return err
	}
	defer src.Close()
	var has int
	if err := src.QueryRowContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/scan_backtest_result_asset_performance_summary")).Scan(&has); err != nil || has == 0 {
		return nil // not a backtest result file
	}
	rows, err := src.QueryContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/scan_backtest_result_asset_performance_summary_2"))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var found [][]any
	for rows.Next() {
		var id string
		var trades int
		var rate sql.NullFloat64
		var start, end sql.NullString
		if err := rows.Scan(&id, &trades, &rate, &start, &end); err != nil {
			return err
		}
		if !want[id] {
			continue
		}
		found = append(found, []any{id, f.pass, f.run, f.path, f.rel, trades, rate, start, end})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := calc.InsertRows(ctx, "bs_found", []string{"strategy_id", "pass", "run", "path", "rel", "trades", "win_rate", "window_start", "window_end"}, found); err != nil {
		return err
	}
	// The trades of the run the summary describes.
	tr, err := src.QueryContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/scan_backtest_result_asset_performance_summary_3"))
	if err != nil {
		return nil // a result file with no trades table has no curve to draw
	}
	defer tr.Close()
	var picked [][]any
	for tr.Next() {
		var id, sym, date string
		var win int
		var ret sql.NullFloat64
		if err := tr.Scan(&id, &sym, &date, &win, &ret); err != nil {
			return err
		}
		if !want[id] {
			continue
		}
		picked = append(picked, []any{id, f.pass, f.run, sym, date, win, ret})
	}
	if err := tr.Err(); err != nil {
		return err
	}
	return calc.InsertRows(ctx, "bs_trades", []string{"strategy_id", "pass", "run", "symbol", "exit_date", "win", "return_pct"}, picked)
}

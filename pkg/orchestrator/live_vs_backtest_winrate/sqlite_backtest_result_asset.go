package live_vs_backtest_winrate

// Backtest ASSETS the refresh and the comparison read: backtestgosqlite's result databases
// (<reports>/<run>/*.db and oos/*.db), its strategy_evals database and a refdata strategies.db.
// They are opened read-only, one query each (sql/asset_*.sql), and only the columns the
// comparison needs are copied into Turso. Nothing here writes an asset.

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	_ "modernc.org/sqlite"
)

type reportDB struct {
	path, pass string // pass is "is" or "oos"
	run        int
}

// reportDBs lists the backtest result ASSETS (result databases) under a reports folder, oldest run first.
func reportDBs(dir string) ([]reportDB, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: %w", err)
	}
	var out []reportDB
	for _, e := range entries {
		run, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		for _, k := range []struct{ sub, pass string }{{"", "is"}, {"oos", "oos"}} {
			dbs, _ := filepath.Glob(filepath.Join(dir, e.Name(), k.sub, "*.db"))
			for _, p := range dbs {
				out = append(out, reportDB{path: p, pass: k.pass, run: run})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].run < out[j].run })
	return out, nil
}

// openBacktestAssetReadOnly opens a backtest asset file so it cannot be changed from here.
func openBacktestAssetReadOnly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}).String())
	if err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: open %s: %w", path, err)
	}
	return db, nil
}

func assetHasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, orchestratorsql.Q("live_vs_backtest_winrate/has_table"), name).Scan(&n)
	return n > 0, err
}

func assetQuery(name string) string {
	b, err := fs.ReadFile(sqlFiles, name)
	if err != nil {
		panic(err) // the file is embedded: a missing one is a build error
	}
	return string(b)
}

// evalAssetRow is the newest evaluation of one strategy in a strategy_evals ASSET.
type evalAssetRow struct {
	ID                               string
	ISStart, ISEnd, OOSStart, OOSEnd sql.NullString
	ISTrades, OOSTrades              int
	ISWinRate, OOSWinRate            sql.NullFloat64
	EvaluatedAt                      sql.NullString
}

func readStrategyEvalsAssetLatest(ctx context.Context, path string) ([]evalAssetRow, error) {
	db, err := openBacktestAssetReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, assetQuery("asset_evals_latest.sql"))
	if err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: %s: %w", path, err)
	}
	defer rows.Close()
	var out []evalAssetRow
	for rows.Next() {
		var r evalAssetRow
		if err := rows.Scan(&r.ID, &r.ISStart, &r.ISEnd, &r.OOSStart, &r.OOSEnd, &r.ISTrades, &r.ISWinRate, &r.OOSTrades, &r.OOSWinRate, &r.EvaluatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// lastBacktestAssetRow is one strategy's last whole-window backtest in a refdata ASSET.
type lastBacktestAssetRow struct {
	ID        string
	Trades    int
	WinRate   sql.NullFloat64
	CreatedAt sql.NullString
}

// readStrategyLastBacktestAsset reads strategy_last_backtest from the refdata asset at path. The
// bool is false when the file has no such table.
func readStrategyLastBacktestAsset(ctx context.Context, path string) ([]lastBacktestAssetRow, bool, error) {
	db, err := openBacktestAssetReadOnly(path)
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	has, err := assetHasTable(ctx, db, "strategy_last_backtest")
	if err != nil || !has {
		return nil, false, err
	}
	rows, err := db.QueryContext(ctx, assetQuery("asset_last_backtest.sql"))
	if err != nil {
		return nil, true, fmt.Errorf("live_vs_backtest_winrate: %s: %w", path, err)
	}
	defer rows.Close()
	var out []lastBacktestAssetRow
	for rows.Next() {
		var r lastBacktestAssetRow
		if err := rows.Scan(&r.ID, &r.Trades, &r.WinRate, &r.CreatedAt); err != nil {
			return nil, true, err
		}
		out = append(out, r)
	}
	return out, true, rows.Err()
}

// summaryAssetRow is one strategy's summary in a backtest result ASSET.
type summaryAssetRow struct {
	ID         string
	Start, End sql.NullString
	Trades     int
	WinRate    sql.NullFloat64
	CreatedAt  sql.NullString
}

// readPerformanceSummaryAsset reads performance_summary from the result asset at path. A file
// without that table is not a backtest result and yields no rows.
func readPerformanceSummaryAsset(ctx context.Context, path string) ([]summaryAssetRow, error) {
	db, err := openBacktestAssetReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	has, err := assetHasTable(ctx, db, "performance_summary")
	if err != nil || !has {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, assetQuery("asset_performance_summary.sql"))
	if err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: %s: %w", path, err)
	}
	defer rows.Close()
	var out []summaryAssetRow
	for rows.Next() {
		var r summaryAssetRow
		if err := rows.Scan(&r.ID, &r.Start, &r.End, &r.Trades, &r.WinRate, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

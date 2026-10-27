package live_vs_backtest_winrate

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
)

// baselineJSON is the backtest win rate of the deployed strategies, compiled into the binary so
// /live_vs_backtest_winrate shows them on a fresh deploy with no refresh run. A refresh into live_vs_backtest_winrate
// overrides it per strategy. Regenerate it with `orchestrator live_vs_backtest_winrate -export-baseline <file>`
// (see ExportBaseline) after `live_vs_backtest_sync`, then rebuild.
//
//go:embed baseline.json
var baselineJSON []byte

// BaselineRow is one strategy's backtest rates, as stored in baseline.json.
type BaselineRow struct {
	Strategy    string   `json:"strategy_id"`
	ISTrades    int      `json:"is_trades"`
	ISWinRate   *float64 `json:"is_win_rate"`
	OOSTrades   int      `json:"oos_trades"`
	OOSWinRate  *float64 `json:"oos_win_rate"`
	AllTrades   int      `json:"all_trades"`
	AllWinRate  *float64 `json:"all_win_rate"`
	ISStart     string   `json:"is_start"`
	ISEnd       string   `json:"is_end"`
	OOSStart    string   `json:"oos_start"`
	OOSEnd      string   `json:"oos_end"`
	EvaluatedAt string   `json:"evaluated_at"`
	AllEvalAt   string   `json:"all_evaluated_at"`
	// Trades are the backtest trades behind the in-sample and out-of-sample rates, for the curves.
	Trades []BaselineTrade `json:"trades,omitempty"`
}

// BaselineTrade is one backtest trade; Pass is "is" or "oos".
type BaselineTrade struct {
	Pass   string   `json:"pass"`
	Symbol string   `json:"symbol"`
	Date   string   `json:"exit_date"`
	Win    bool     `json:"win"`
	Return *float64 `json:"return_pct,omitempty"`
}

// Baseline returns the compiled-in rows.
func Baseline() ([]BaselineRow, error) {
	var rows []BaselineRow
	if len(baselineJSON) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(baselineJSON, &rows); err != nil {
		return nil, fmt.Errorf("live_vs_backtest_winrate: baseline.json: %w", err)
	}
	return rows, nil
}

// loadBaseline puts the compiled-in rows in winrate_baseline for the slice SQL.
func loadBaseline(ctx context.Context, run *runtables.Run) error {
	if err := run.Exec(ctx, `DROP TABLE IF EXISTS winrate_baseline;
CREATE TABLE winrate_baseline (strategy_id TEXT, is_trades INTEGER, is_win_rate REAL, oos_trades INTEGER, oos_win_rate REAL,
	all_trades INTEGER, all_win_rate REAL, is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT, evaluated_at TEXT, all_evaluated_at TEXT);
DROP TABLE IF EXISTS winrate_baseline_trade;
CREATE TABLE winrate_baseline_trade (strategy_id TEXT, pass TEXT, symbol TEXT, exit_date TEXT, win INTEGER, return_pct REAL)`); err != nil {
		return err
	}
	rows, err := Baseline()
	if err != nil {
		return err
	}
	var base, trades [][]any
	for _, r := range rows {
		for _, t := range r.Trades {
			win := 0
			if t.Win {
				win = 1
			}
			trades = append(trades, []any{r.Strategy, t.Pass, t.Symbol, t.Date, win, t.Return})
		}
		base = append(base, []any{r.Strategy, r.ISTrades, r.ISWinRate, r.OOSTrades, r.OOSWinRate, r.AllTrades, r.AllWinRate,
			r.ISStart, r.ISEnd, r.OOSStart, r.OOSEnd, r.EvaluatedAt, r.AllEvalAt})
	}
	if err := run.InsertRows(ctx, "winrate_baseline_trade", []string{"strategy_id", "pass", "symbol", "exit_date", "win", "return_pct"}, trades); err != nil {
		return err
	}
	return run.InsertRows(ctx, "winrate_baseline", []string{"strategy_id", "is_trades", "is_win_rate", "oos_trades", "oos_win_rate", "all_trades", "all_win_rate",
		"is_start", "is_end", "oos_start", "oos_end", "evaluated_at", "all_evaluated_at"}, base)
}

// ExportBaseline writes live_vs_backtest_winrate, for the strategies in ids (all when empty), to path in
// the form baseline.json takes. Nothing is written for a strategy with no backtest rate.
func ExportBaseline(ctx context.Context, db *sql.DB, path string, ids []string) (int, error) {
	wanted := func(id string) bool { return len(ids) == 0 || slices.Contains(ids, id) }
	rows, err := db.QueryContext(ctx, `SELECT strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, COALESCE(all_trades, 0), all_win_rate,
		COALESCE(is_start, ''), COALESCE(is_end, ''), COALESCE(oos_start, ''), COALESCE(oos_end, ''), COALESCE(evaluated_at, ''), COALESCE(all_evaluated_at, '')
		FROM live_vs_backtest_winrate
		WHERE (is_win_rate IS NOT NULL OR oos_win_rate IS NOT NULL OR all_win_rate IS NOT NULL)
		ORDER BY strategy_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	out := []BaselineRow{}
	for rows.Next() {
		var r BaselineRow
		var is, oos, all sql.NullFloat64
		if err := rows.Scan(&r.Strategy, &r.ISTrades, &is, &r.OOSTrades, &oos, &r.AllTrades, &all,
			&r.ISStart, &r.ISEnd, &r.OOSStart, &r.OOSEnd, &r.EvaluatedAt, &r.AllEvalAt); err != nil {
			return 0, err
		}
		r.ISWinRate, r.OOSWinRate, r.AllWinRate = ptr(is), ptr(oos), ptr(all)
		if wanted(r.Strategy) {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()
	if has, err := hasTable(ctx, db, "live_vs_backtest_deployed_trade"); err != nil {
		return 0, err
	} else if has {
		at := map[string]int{}
		for i, r := range out {
			at[r.Strategy] = i
		}
		tr, err := db.QueryContext(ctx, `SELECT strategy_id, pass, symbol, exit_date, win, return_pct FROM live_vs_backtest_deployed_trade ORDER BY strategy_id, pass, exit_date, symbol`)
		if err != nil {
			return 0, err
		}
		defer tr.Close()
		for tr.Next() {
			var id string
			var t BaselineTrade
			var win int
			var ret sql.NullFloat64
			if err := tr.Scan(&id, &t.Pass, &t.Symbol, &t.Date, &win, &ret); err != nil {
				return 0, err
			}
			t.Win, t.Return = win == 1, ptr(ret)
			if i, ok := at[id]; ok {
				out[i].Trades = append(out[i].Trades, t)
			}
		}
		if err := tr.Err(); err != nil {
			return 0, err
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return 0, err
	}
	return len(out), os.WriteFile(path, append(b, '\n'), 0o644)
}

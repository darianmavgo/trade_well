package park_sweep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReportSnapshotsTheSweep(t *testing.T) {
	dir := t.TempDir()
	sweep := filepath.Join(dir, "park.db")
	report := filepath.Join(dir, "park_report.db")
	htmlPath := filepath.Join(dir, "park.html")
	db, err := Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sweep_config (id, start_date, end_date, capital, park_symbol)
		VALUES (1, '2021-10-01', '2021-10-06', 100000, 'GOOGL')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO park_asset (symbol, first_date, last_date, bar_count, final_equity, cagr, max_drawdown_pct, sharpe, dividends)
		VALUES ('GOOGL', '2021-10-01', '2021-10-06', 6, 150000, 0.2, 0.1, 1.2, 10)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sweep_strategy (
		strategy_id, kind, name, signal_symbol, trade_symbol, direction, signal_days,
		hold_days, take_profit_pct, stop_loss_pct, regime, target_state,
		allocation_pct, cash_yield, slippage_pct, commission_per_share, next_day_limit)
		VALUES ('streak-aaa-down1-aaa', 'streak', 'AAA', 'AAA', 'AAA', 'drop', 1, 1, 0, 0, 'All Regimes', '', 0.1, 0, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO strategy_run (
		strategy_id, status, final_equity, cagr, max_drawdown_pct, sharpe, total_trades,
		winning_trades, losing_trades, win_rate, sleeve_net, park_contribution, edge_vs_googl)
		VALUES ('streak-aaa-down1-aaa', 'done', 160000, 0.25, 0.05, 1.1, 3, 2, 1, 0.66, 4000, 56000, 10000)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if err := WriteReport(sweep, report, htmlPath, nil); err != nil {
		t.Fatal(err)
	}
	out, err := Open(report)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var n int
	var equity float64
	if err := out.QueryRow(`SELECT COUNT(*) FROM strategy_result`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("strategy_result rows = %d", n)
	}
	if err := out.QueryRow(`SELECT hold_final_equity FROM report WHERE id = 1`).Scan(&equity); err != nil {
		t.Fatal(err)
	}
	if equity != 150000 {
		t.Fatalf("hold equity = %v", equity)
	}
	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "streak-aaa-down1-aaa") || !strings.Contains(string(body), "150000") {
		t.Fatalf("html missing sweep figures:\n%s", body)
	}
}

package live_vs_backtest_slippage

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
	_ "modernc.org/sqlite"
)

func TestRunSummarizesSlippage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := live_vs_backtest_trades.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO live_vs_backtest_trade (
		account_number, strategy_id, symbol, as_of_date, live_outcome,
		entry_fill_price, exit_fill_price, fees,
		entry_slippage_bps, exit_slippage_bps, entry_vs_limit_bps, fill_delay_sessions,
		built_at
	) VALUES ('1', 'tsll-daily-one-share', 'TSLL', '2026-09-24', 'CLOSED', 10.10, 10.20, 0.03, 80, 20, 100, 0, '2026-09-29T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d", len(res.Rows))
	}
	got := res.Rows[0]
	if got.StrategyID != "tsll-daily-one-share" || got.Fills != 1 {
		t.Fatalf("row %+v", got)
	}
	if !got.AvgEntry.Valid || math.Abs(got.AvgEntry.Float64-80) > 1e-6 {
		t.Fatalf("avg entry %+v", got.AvgEntry)
	}
	if !got.AvgExit.Valid || math.Abs(got.AvgExit.Float64-20) > 1e-6 {
		t.Fatalf("avg exit %+v", got.AvgExit)
	}
	if !got.WorstEntry.Valid || math.Abs(got.WorstEntry.Float64-80) > 1e-6 {
		t.Fatalf("worst %+v", got.WorstEntry)
	}
	if math.Abs(got.Fees-0.03) > 1e-6 {
		t.Fatalf("fees %v", got.Fees)
	}
	if len(res.Worst) != 1 || math.Abs(res.Worst[0].EntryFill-10.10) > 1e-6 {
		t.Fatalf("worst fills %+v", res.Worst)
	}
}

func TestRunRequiresALedger(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = Run(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "live_vs_backtest_trades") {
		t.Fatalf("err = %v", err)
	}
}

package live_vs_backtest_trades

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	_ "modernc.org/sqlite"
)

func TestRunReconcilesAFilledTradeAndADismissedSignal(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := scan_market.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := stage_orders.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := account_state.MirrorStore().EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	seedLiveBook(t, db)

	bars := map[string][]models.Bar{
		"TSLL": {
			{Symbol: "TSLL", Date: "2026-09-24", Open: 10, High: 10.2, Low: 9.9, Close: 10, Volume: 1000},
			{Symbol: "TSLL", Date: "2026-09-25", Open: 10.05, High: 10.3, Low: 9.9, Close: 10.2, Volume: 1000},
		},
	}
	// tsll-daily-one-share calculates its signals in SQL from a market database, so write the
	// same bars into a real one.
	marketDB := filepath.Join(t.TempDir(), "market.db")
	market, err := storage.OpenSQLite(marketDB)
	if err != nil {
		t.Fatal(err)
	}
	defer market.Close()
	if err := storage.EnsureBarTable(market, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	if err := storage.UpsertBars(market, "backtest_start", bars["TSLL"]); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), db, "", marketDB, bars)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}

	filled := findRow(res.Rows, "2026-09-24")
	if math.Abs(filled.entryFill-10.10) > 1e-6 {
		t.Fatalf("entry fill = %v, want 10.10", filled.entryFill)
	}
	if math.Abs(filled.fees-0.03) > 1e-6 {
		t.Fatalf("fees = %v, want 0.03", filled.fees)
	}
	if !filled.entryBps.Valid || filled.entryBps.Float64 <= 0 {
		t.Fatalf("entry slippage = %+v, want positive", filled.entryBps)
	}
	if filled.signalMatch != 1 || filled.outcomeMatch != 1 {
		t.Fatalf("match signal/outcome = %d/%d, live %s model %s/%s", filled.signalMatch, filled.outcomeMatch, filled.liveOutcome, filled.modelOutcome, filled.modelReason)
	}
	if filled.liveOutcome != "CLOSED" || filled.modelOutcome != "CLOSED" || filled.modelReason != "TIME_UP" {
		t.Fatalf("outcomes live %s model %s %s", filled.liveOutcome, filled.modelOutcome, filled.modelReason)
	}
	if math.Abs(filled.exitFill-10.20) > 1e-6 {
		t.Fatalf("exit fill = %v, want 10.20", filled.exitFill)
	}

	skipped := findRow(res.Rows, "2026-09-23")
	if skipped.liveOutcome != "SKIPPED" || skipped.outcomeMatch != 0 {
		t.Fatalf("dismissed live %s match %d", skipped.liveOutcome, skipped.outcomeMatch)
	}

	var stored float64
	var match int
	if err := db.QueryRow(`SELECT entry_fill_price, outcome_match FROM live_vs_backtest_trade WHERE as_of_date = '2026-09-24'`).Scan(&stored, &match); err != nil {
		t.Fatal(err)
	}
	if math.Abs(stored-10.10) > 1e-6 || match != 1 {
		t.Fatalf("stored fill %v match %d", stored, match)
	}
}

func findRow(rows []ledger, asOf string) ledger {
	for _, r := range rows {
		if r.asOf == asOf {
			return r
		}
	}
	return ledger{}
}

func seedLiveBook(t *testing.T, db *sql.DB) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO stage_order (
		account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price,
		max_trading_days, status, order_id, staged_at
	) VALUES ('1', 'tsll-daily-one-share', 'TSLL', 1, 10, 10.5, 8, 1, 'SUBMITTED', '55', '2026-09-24')`)
	if err != nil {
		t.Fatal(err)
	}
	stageID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scan_market_buy_signal (
		scan_run_id, strategy_id, symbol, as_of_date, next_session, close, buy_limit,
		take_profit, stop_loss, hold_days, status, staged_order_id, notes, created_at
	) VALUES ('t', 'tsll-daily-one-share', 'TSLL', '2026-09-24', '2026-09-25', 10, 10, 10.5, 8, 1, 'STAGED', ?, '', '2026-09-24')`, stageID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scan_market_buy_signal (
		scan_run_id, strategy_id, symbol, as_of_date, next_session, close, buy_limit,
		take_profit, stop_loss, hold_days, status, notes, created_at
	) VALUES ('t', 'tsll-daily-one-share', 'TSLL', '2026-09-23', '2026-09-24', 10, 10, 10.5, 8, 1, 'DISMISSED', 'no cash', '2026-09-23')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price,
		quantity, entry_time, max_trading_days, status, exit_reason_code, entry_filled_at, exit_date
	) VALUES ('1', 'TSLL', 'tsll-daily-one-share', '55', 10, 10.5, 1, '2026-09-25T14:30:00Z', 1, 'CLOSED_TIME_EXPIRATION', 'TIME_UP', '2026-09-25T14:30:00Z', '2026-09-25')`); err != nil {
		t.Fatal(err)
	}
	raw := `{
		"orderLegCollection": [{"instruction": "BUY"}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 10.10, "quantity": 1}]}],
		"childOrderStrategies": [{
			"orderLegCollection": [{"instruction": "SELL"}],
			"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 10.20, "quantity": 1}]}]
		}]
	}`
	if _, err := db.Exec(`INSERT INTO account_state_order (order_id, account_number, raw_json, fetched_at)
		VALUES ('55', '1', ?, '2026-09-25T15:00:00Z')`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO account_state_transaction (activity_id, account_number, trade_date, fees, symbol, fetched_at)
		VALUES ('a1', '1', '2026-09-25', 0.03, 'TSLL', '2026-09-25T15:00:00Z')`); err != nil {
		t.Fatal(err)
	}
}

// A staged order with no scan_market_buy_signal (the orders reopened by hand) must not fail the
// backfill: buy_signal_id is NOT NULL, so "no signal" is 0.
func TestBackfillFillsAStagedOrderWithNoBuySignal(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := scan_market.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := stage_orders.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO stage_order (
		account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price,
		max_trading_days, status, order_id, staged_at
	) VALUES ('1', 'hmm-normal-tecl', 'TECL', 1, 10, 0, 9.8, 8, 'SUBMITTED', '77', '2026-10-26')`)
	if err != nil {
		t.Fatal(err)
	}
	stageID, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price,
		quantity, entry_time, max_trading_days, status, stage_order_id
	) VALUES ('1', 'TECL', 'hmm-normal-tecl', '77', 10, 0, 1, '2026-10-26T14:30:00Z', 8, 'ACTIVE', ?)`, stageID); err != nil {
		t.Fatal(err)
	}
	if err := backfillFills(db); err != nil {
		t.Fatalf("backfill failed on a staged order with no buy signal: %v", err)
	}
	var id int64
	if err := db.QueryRow(`SELECT buy_signal_id FROM manage_exit WHERE entry_order_id = '77'`).Scan(&id); err != nil || id != 0 {
		t.Fatalf("buy_signal_id = %d, err %v; want 0", id, err)
	}
}

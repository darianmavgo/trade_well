package live_vs_backtest_report

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	_ "modernc.org/sqlite"
)

func TestRunWritesCashAndHTML(t *testing.T) {
	dir := t.TempDir()
	orchPath := filepath.Join(dir, "orchestrator.db")
	marketPath := filepath.Join(dir, "market.db")
	htmlPath := filepath.Join(dir, "live_vs_backtest_report.html")

	db, err := sql.Open("sqlite", orchPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := account_state.MirrorStore().EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	seedReportBook(t, db)
	if err := seedMarket(marketPath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := Run(context.Background(), &out, jobenv.Env{
		DB: db,
		Cfg: &appconfig.Config{
			DBPath:            orchPath,
			MarketDB:          marketPath,
			StrategyAllowlist: []string{"tsll-daily-one-share"},
		},
	}, Options{
		MarketDB:  marketPath,
		Table:     "backtest_start",
		HTML:      htmlPath,
		Allowlist: []string{"tsll-daily-one-share"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunID == "" || res.HTML != htmlPath {
		t.Fatalf("result %+v", res)
	}
	if res.RoundTrips != 2 {
		t.Fatalf("round trips = %d, want 2", res.RoundTrips)
	}
	summary := FormatStatus(res)
	for _, want := range []string{
		"live_vs_backtest_report OK, with warnings",
		htmlPath,
		"tables __" + res.RunID,
		"stage_order is empty",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("status missing %q\n%s", want, summary)
		}
	}

	// The report tables are ordinary tables of the orchestrator database, named for this run.
	tbl := func(name string) string { return name + "__" + res.RunID }
	report := db

	var tsllCash, tsllFees float64
	var tsllGreen int
	var tsllStrategy string
	if err := report.QueryRow(`SELECT cash_pnl, fees, greenlit, strategy_id FROM `+tbl("live_vs_backtest_report_round_trip")+` WHERE symbol = 'TSLL'`).
		Scan(&tsllCash, &tsllFees, &tsllGreen, &tsllStrategy); err != nil {
		t.Fatal(err)
	}
	if math.Abs(tsllCash-0.09) > 1e-6 || tsllGreen != 1 || tsllStrategy != "tsll-daily-one-share" {
		t.Fatalf("TSLL cash %v greenlit %d strategy %s fees %v", tsllCash, tsllGreen, tsllStrategy, tsllFees)
	}
	if math.Abs(tsllFees-0.01) > 1e-6 {
		t.Fatalf("TSLL fees %v, want 0.01", tsllFees)
	}

	var maraCash, maraFees float64
	var maraGreen int
	var maraStrategy string
	if err := report.QueryRow(`SELECT cash_pnl, fees, greenlit, strategy_id FROM `+tbl("live_vs_backtest_report_round_trip")+` WHERE symbol = 'MARA'`).
		Scan(&maraCash, &maraFees, &maraGreen, &maraStrategy); err != nil {
		t.Fatal(err)
	}
	if math.Abs(maraCash-(-2)) > 1e-6 || maraGreen != 0 || maraStrategy != "" {
		t.Fatalf("MARA cash %v greenlit %d strategy %q fees %v", maraCash, maraGreen, maraStrategy, maraFees)
	}
	if math.Abs(maraFees-0.63) > 1e-6 {
		t.Fatalf("MARA fees %v, want 0.63 already inside the nets", maraFees)
	}

	var greenlit float64
	if err := report.QueryRow(`SELECT SUM(cash_pnl) FROM ` + tbl("live_vs_backtest_report_round_trip") + ` WHERE greenlit = 1 AND sell_order_id != ''`).Scan(&greenlit); err != nil {
		t.Fatal(err)
	}
	if math.Abs(greenlit-0.09) > 1e-6 {
		t.Fatalf("greenlit cash %v, want 0.09", greenlit)
	}

	var decisions int
	if err := report.QueryRow(`SELECT COUNT(*) FROM live_vs_backtest_trade`).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions < 1 {
		t.Fatal("live_vs_backtest_trade is empty")
	}
	// Nothing is copied from the account mirror or the market file into report tables.
	for _, copied := range []string{"live_vs_backtest_report_account", "live_vs_backtest_report_position", "live_vs_backtest_report_bar", "live_vs_backtest_report_reconcile", "live_vs_backtest_report_slippage"} {
		var n int
		if err := report.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE ?`, copied+"%").Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s tables = %d (%v), want none: the report reads the originals", copied, n, err)
		}
	}

	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"tsll-daily-one-share",
		"MARA",
		"+$0.09",
		"−$2.00",
		"stage_order is empty, so filled trades have no stored signal date and the replay dates them on the fill day",
		"still open in manage_exit after the broker sold them",
		"have no manage_exit row, so live_vs_backtest_trades did not replay them",
		"orchestrator live_vs_backtest_report",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 1`, JobName).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OK" {
		t.Fatalf("job_run status %s", status)
	}
}

func TestFormatStatus(t *testing.T) {
	clean := FormatStatus(Result{Decisions: 3, RoundTrips: 1, RunID: "r1", HTML: "a.html"})
	if !strings.HasPrefix(clean, "live_vs_backtest_report OK\n") || strings.Contains(clean, "with warnings") {
		t.Fatalf("clean status:\n%s", clean)
	}
	warned := FormatStatus(Result{Warnings: []string{"market db missing"}})
	if !strings.HasPrefix(warned, "live_vs_backtest_report OK, with warnings\n") || !strings.Contains(warned, "warning: market db missing") {
		t.Fatalf("warned status:\n%s", warned)
	}
}

func seedReportBook(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price,
		quantity, entry_time, max_trading_days, status
	) VALUES ('1', 'TSLL', 'tsll-daily-one-share', '55', 10.10, 10.60, 1, '2026-09-24T16:06:00Z', 1, 'OPEN')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO account_state_account (
		account_number, account_type, cash_balance, liquidation_value, fetched_at
	) VALUES ('1', 'MARGIN', 100, 27431.92, '2026-09-29T20:15:00Z')`); err != nil {
		t.Fatal(err)
	}
	tsll := `{
		"orderId": 55,
		"closeTime": "2026-09-24T16:06:00Z",
		"orderLegCollection": [{"instruction": "BUY", "instrument": {"symbol": "TSLL"}}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 10.10, "quantity": 1}]}],
		"childOrderStrategies": [{
			"orderId": 56,
			"closeTime": "2026-09-25T13:30:00Z",
			"orderLegCollection": [{"instruction": "SELL", "instrument": {"symbol": "TSLL"}}],
			"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 10.19, "quantity": 1}]}]
		}]
	}`
	maraBuy := `{
		"orderId": 70,
		"closeTime": "2026-09-24T13:30:00Z",
		"orderLegCollection": [{"instruction": "BUY", "instrument": {"symbol": "MARA"}}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 13, "quantity": 2}]}]
	}`
	maraSell := `{
		"orderId": 71,
		"closeTime": "2026-09-25T13:35:00Z",
		"orderLegCollection": [{"instruction": "SELL", "instrument": {"symbol": "MARA"}}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 12, "quantity": 2}]}]
	}`
	if _, err := db.Exec(`INSERT INTO account_state_order (order_id, account_number, raw_json, fetched_at) VALUES
		('55', '1', ?, '2026-09-29T20:15:00Z'),
		('70', '1', ?, '2026-09-29T20:15:00Z'),
		('71', '1', ?, '2026-09-29T20:15:00Z')`, tsll, maraBuy, maraSell); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO account_state_transaction (
		activity_id, account_number, transaction_type, net_amount, fees, raw_json, trade_date, symbol, fetched_at
	) VALUES
		('t55', '1', 'TRADE', -10.10, 0.01, '{"orderId": 55}', '2026-09-24', 'TSLL', '2026-09-29T20:15:00Z'),
		('t56', '1', 'TRADE', 10.19, 0, '{"orderId": 56}', '2026-09-25', 'TSLL', '2026-09-29T20:15:00Z'),
		('t70', '1', 'TRADE', -26, 0, '{"orderId": 70}', '2026-09-24', 'MARA', '2026-09-29T20:15:00Z'),
		('t71', '1', 'TRADE', 24, 0.63, '{"orderId": 71}', '2026-09-25', 'MARA', '2026-09-29T20:15:00Z'),
		('div', '1', 'DIVIDEND', 1000, 0, '{"orderId": 70}', '2026-09-25', 'MARA', '2026-09-29T20:15:00Z')`); err != nil {
		t.Fatal(err)
	}
}

func seedMarket(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (
		idx INTEGER,
		symbol TEXT,
		Date TEXT,
		open REAL,
		high REAL,
		low REAL,
		close REAL,
		volume REAL,
		"Adj Close" REAL
	)`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO backtest_start (idx, symbol, Date, open, high, low, close, volume, "Adj Close") VALUES
		(1, 'TSLL', '2026-09-23', 10, 10.2, 9.8, 10, 1000, 10),
		(2, 'TSLL', '2026-09-24', 10, 10.3, 9.9, 10.1, 1000, 10.1),
		(3, 'TSLL', '2026-09-25', 10.1, 10.4, 10, 10.2, 1000, 10.2),
		(4, 'MARA', '2026-09-23', 13, 13.4, 12.6, 13, 1000, 13),
		(5, 'MARA', '2026-09-24', 13, 13.2, 12.4, 12.8, 1000, 12.8),
		(6, 'MARA', '2026-09-25', 12.8, 12.9, 12, 12.2, 1000, 12.2)`)
	return err
}

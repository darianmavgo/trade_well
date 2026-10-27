package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_winrate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

func TestLiveVsBacktestWinratePageIsHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	srv, h := checkHealthServer(t)
	if err := live_vs_backtest_trades.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	if err := live_vs_backtest_winrate.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Exec(`INSERT INTO live_vs_backtest_winrate (strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, synced_at) VALUES ('pdd_tree', 90, 0.64, 30, 0.6, 'now')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		pnl := -1.0
		if i < 3 {
			pnl = 2
		}
		if _, err := srv.db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, pnl, pnl_pct, entry_price, target_profit_price, stop_loss_price, quantity, entry_time, max_trading_days)
			VALUES ('1', 'PDD', 'pdd_tree', 'pdd_tree', ?, 'CLOSED_TARGET', ?, ?, 10, 11, 9, 1, ?, 1)`, fmt.Sprint("o", i), pnl, pnl, fmt.Sprintf("2026-09-%02d 13:30:00 +0000 +0000", i+1)); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.db.Exec(`INSERT INTO live_vs_backtest_trade (strategy_id, symbol, as_of_date, live_outcome, live_return_pct, model_outcome, model_return_pct, built_at)
			VALUES ('pdd_tree', 'PDD', ?, 'CLOSED', ?, 'CLOSED', 0.02, 'now')`, fmt.Sprint("2026-09-", i+10), pnl); err != nil {
			t.Fatal(err)
		}
	}

	w := getStatus(h, "/live_vs_backtest_winrate")
	body := w.Body.String()
	if !strings.Contains(body, "Not greenlit, but with results (1)") {
		t.Error("strategy with data that is not on the allowlist is not in its own section")
	}
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("default = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"<title>live_vs_backtest_winrate</title>", `href="/status/pdd_tree"`, "In-sample", "Out-of-sample", "<b>3/12</b>", "25% win rate", "BELOW", "look at fills", "?format=json", `<svg class="chart"`, "<polyline", "<polygon", "in-sample 64%", "out-of-sample 60%", "Decision 12, PDD", "width:25.0%"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("a style or attribute value was rejected by the template escaper")
	}
	if n := strings.Count(body, `class="dot win"`); n != 3 {
		t.Errorf("win dots = %d, want 3", n)
	}
	if n := strings.Count(body, `class="dot loss"`); n != 9 {
		t.Errorf("loss dots = %d, want 9", n)
	}
	if strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Error("default response is raw JSON")
	}

	j := getStatus(h, "/live_vs_backtest_winrate?format=json")
	if !strings.HasPrefix(j.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("format=json content type %q", j.Header().Get("Content-Type"))
	}
	var got struct {
		Strategies   []live_vs_backtest_winrate.Row   `json:"strategies"`
		RecentDays   int                              `json:"recent_days"`
		RecentTrades []live_vs_backtest_winrate.Trade `json:"recent_trades"`
	}
	if err := json.Unmarshal(j.Body.Bytes(), &got); err != nil || len(got.Strategies) != 1 || got.Strategies[0].Verdict != live_vs_backtest_winrate.Below || got.Strategies[0].LiveDecisions != 12 || got.RecentDays != 3 {
		t.Errorf("json = %+v err %v", got, err)
	}
	if got.RecentTrades == nil {
		t.Error("recent_trades is null, want an empty list")
	}
}

// A greenlit strategy with no results is still listed, not hidden.
func TestLiveVsBacktestWinratePageListsGreenlitStrategiesWithNoData(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "never_backtested_a,never_backtested_b")
	_, h := checkHealthServer(t)
	body := getStatus(h, "/live_vs_backtest_winrate").Body.String()
	for _, id := range []string{"never_backtested_a", "never_backtested_b"} {
		if !strings.Contains(body, `href="/status/`+id+`"`) {
			t.Errorf("no card for %s", id)
		}
	}
	if !strings.Contains(body, "Greenlit (2)") || strings.Contains(body, "Not greenlit") {
		t.Error("greenlit section missing or wrong")
	}
	if strings.Contains(body, "<svg") {
		t.Error("a chart was drawn for strategies with no closed live trades")
	}
	if !strings.Contains(body, "NO_BACKTEST") || !strings.Contains(body, "Nothing to chart yet") {
		t.Error("cards do not say what is missing")
	}
}

func TestLiveVsBacktestWinratePageEmptyState(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "")
	_, h := checkHealthServer(t)
	w := getStatus(h, "/live_vs_backtest_winrate")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No greenlit strategies") {
		t.Errorf("empty = %d %q", w.Code, w.Body.String())
	}
}

// The deployed case: a database nothing was ever refreshed into still shows the backtest rates of
// the greenlit strategies, because they are compiled into the binary.
func TestLiveVsBacktestWinratePageShowsBundledBacktestRatesOnAFreshDatabase(t *testing.T) {
	base, err := live_vs_backtest_winrate.Baseline()
	if err != nil || len(base) == 0 {
		t.Fatalf("baseline = %v, %v", base, err)
	}
	var ids []string
	for _, b := range base {
		ids = append(ids, b.Strategy)
	}
	t.Setenv("STRATEGY_ALLOWLIST", strings.Join(ids, ","))
	_, h := checkHealthServer(t)
	body := getStatus(h, "/live_vs_backtest_winrate").Body.String()
	if strings.Contains(body, "NO_BACKTEST") {
		t.Error("a greenlit strategy with a bundled baseline reads NO_BACKTEST")
	}
	if !strings.Contains(body, "bundled with this deploy") {
		t.Error("the page does not say the backtest rates are bundled")
	}
	// Every strategy with a backtest gets a chart with its two backtest curves, even with no live trade.
	if n := strings.Count(body, `<svg class="chart"`); n != len(base) {
		t.Errorf("charts = %d, want %d (one per strategy with a bundled backtest)", n, len(base))
	}
	if n := strings.Count(body, `class="curve is"`); n != len(base) {
		t.Errorf("in-sample curves = %d, want %d", n, len(base))
	}
	if n := strings.Count(body, `class="curve oos"`); n != len(base) {
		t.Errorf("out-of-sample curves = %d, want %d", n, len(base))
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("a value was rejected by the template escaper")
	}
	for _, b := range base {
		rate := b.OOSWinRate
		if rate == nil {
			rate = b.ISWinRate
		}
		if !strings.Contains(body, fmt.Sprintf("<b>%.0f%%</b>", *rate*100)) {
			t.Errorf("%s: backtest rate %.0f%% is not on the page", b.Strategy, *rate*100)
		}
	}
}

// The stop losses that filled this morning and yesterday are listed, newest first, with the
// account's last four digits, and counted in the heading.
func TestLiveVsBacktestWinratePageListsRecentStopLosses(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "markov_model_amuu")
	srv, h := checkHealthServer(t)
	if err := manage_exits.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, c := range []struct {
		acct   string
		when   time.Time
		status string
		code   string
		pnl    float64
	}{
		{"73890011", now, "CLOSED_STOP_LOSS", "STOP_LOSS", -2445.75},
		{"74234603", now.AddDate(0, 0, -1), "CLOSED_STOP_LOSS", "STOP_LOSS", -108.7},
		{"74234603", now.AddDate(0, 0, -1), "CLOSED_PROFIT", "TARGET", 55},
		{"74234603", now.AddDate(0, 0, -20), "CLOSED_STOP_LOSS", "STOP_LOSS", -9999},
	} {
		if _, err := srv.db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, exit_reason_code, exit_time,
			entry_price, exit_price, quantity, pnl, pnl_pct, target_profit_price, stop_loss_price, entry_time, max_trading_days)
			VALUES (?, 'AMUU', 'markov_model_amuu', 'markov_model_amuu', ?, ?, ?, ?, 271.76, 217.41, 10, ?, -20, 300, 217, 'x', 1)`,
			c.acct, fmt.Sprint("o", i), c.status, c.code, c.when.Format("2006-01-02 15:04:05 +0000 +0000"), c.pnl); err != nil {
			t.Fatal(err)
		}
	}
	body := getStatus(h, "/live_vs_backtest_winrate").Body.String()
	for _, want := range []string{"Closed trades, last 3 days", "2 stop losses", "<b>0/1</b>", "4 account fills", "STOP LOSS", "...0011", "...4603", "-2445.75", "$271.76", "$217.41", "ET</td>", "not the real prices", "limit, not the fill", "order price, not the fill", "orchestrator backfill_fills"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Order: comparison table, then the closed-trade detail, then the charts.
	cmp, detail, chart := strings.Index(body, "Greenlit (1)"), strings.Index(body, "Closed trades, last 3 days"), strings.Index(body, `<svg class="chart"`)
	if cmp < 0 || detail < 0 || chart < 0 || !(cmp < detail && detail < chart) {
		t.Errorf("order wrong: comparison %d, detail %d, chart %d", cmp, detail, chart)
	}
	if strings.Contains(body, "-9999") {
		t.Error("a trade closed 20 days ago is in the 3-day list")
	}
	if i, j := strings.Index(body, "-2445.75"), strings.Index(body, "-108.70"); i < 0 || j < 0 || i > j {
		t.Error("recent trades are not newest first")
	}
	var wide struct {
		RecentTrades []live_vs_backtest_winrate.Trade `json:"recent_trades"`
	}
	if err := json.Unmarshal(getStatus(h, "/live_vs_backtest_winrate?format=json&days=30").Body.Bytes(), &wide); err != nil || len(wide.RecentTrades) != 4 {
		t.Errorf("days=30 trades = %d, %v", len(wide.RecentTrades), err)
	}
	if w := getStatus(h, "/live_vs_backtest_winrate?days=0"); w.Code != 400 {
		t.Errorf("days=0 = %d, want 400", w.Code)
	}
}

// Rows with the broker's fills recorded show them, labelled as fills, and are not flagged.
func TestLiveVsBacktestWinratePageShowsRecordedFillsAndDoesNotFlagThem(t *testing.T) {
	srv, h := checkHealthServer(t)
	if err := manage_exits.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, exit_reason_code, exit_time,
		entry_price, entry_fill_price, exit_price, exit_fill_price, quantity, pnl, pnl_pct, target_profit_price, stop_loss_price, entry_time, max_trading_days)
		VALUES ('75124906', 'AMUU', 'markov_model_amuu', 'markov_model_amuu', 'e1', 'CLOSED_STOP_LOSS', 'STOP_LOSS', ?,
		271.76, 256.69, 213.27, 213.27, 45, -1947.9, -16.9, 300, 217, 'x', 1)`, time.Now().UTC().Format("2006-01-02 15:04:05 +0000 +0000")); err != nil {
		t.Fatal(err)
	}
	body := getStatus(h, "/live_vs_backtest_winrate").Body.String()
	for _, want := range []string{"$256.69", "$213.27", "-16.9%", "-1947.90"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "$271.76") || strings.Contains(body, "not the real prices") || strings.Contains(body, "limit, not the fill") {
		t.Error("a row with recorded fills is still shown from the limit, or flagged as unmeasured")
	}
}

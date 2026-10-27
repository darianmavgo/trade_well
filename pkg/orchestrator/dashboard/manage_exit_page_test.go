package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

func getManageExit(h http.Handler, method, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/manage_exit", nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestManageExitPageRequiresTheDashboardAuth(t *testing.T) {
	_, h := checkHealthServer(t)
	if w := getManageExit(h, "GET", "", ""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no credentials = %d, want 401 with a Basic challenge", w.Code)
	}
	if w := getManageExit(h, "GET", "admin", "wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", w.Code)
	}
	w := getManageExit(h, "GET", "admin", "s3cr3t")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("correct credentials = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestManageExitPageEmptyState(t *testing.T) {
	_, h := checkHealthServer(t)
	body := getManageExit(h, "GET", "admin", "s3cr3t").Body.String()
	for _, want := range []string{"<title>manage_exit</title>", "Open positions", "Entries waiting to fill", "Closed and cancelled", "None."} {
		if !strings.Contains(body, want) {
			t.Errorf("empty page missing %q", want)
		}
	}
}

// Every state lands in its own table, with every column, exit dates included.
func TestManageExitPagePrintsEachStateWithAllItsColumns(t *testing.T) {
	srv, h := checkHealthServer(t)
	loc, _ := time.LoadLocation("America/New_York")
	fill := time.Date(2026, 9, 24, 9, 31, 0, 0, loc)
	mk := func(acct, sym, order string) manage_exits.OpenTradeParams {
		return manage_exits.OpenTradeParams{AccountNumber: acct, Symbol: sym, StrategyName: "tsll-daily-one-share", EntryOrderID: order,
			EntryPrice: 10.30, TargetProfitPrice: 10.82, StopLossPrice: 8.24, Quantity: 1, EntryTime: fill, FilledAt: fill, MaxTradingDays: 1}
	}
	if _, err := manage_exits.Open(srv.db, mk("74234603", "TSLL", "1001")); err != nil {
		t.Fatal(err)
	}
	pend := mk("25474194", "MARA", "2002")
	pend.FilledAt = time.Time{}
	if _, err := manage_exits.RegisterPending(srv.db, pend); err != nil {
		t.Fatal(err)
	}
	id, _ := manage_exits.Open(srv.db, mk("74234603", "PDD", "3003"))
	if err := manage_exits.CloseActiveTrade(srv.db, id, manage_exits.TradeStatusClosedProfit, "PROFIT_TAKER_FILLED", 12.5, fill.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	body := getManageExit(h, "GET", "admin", "s3cr3t").Body.String()
	sections := strings.Split(body, "<h2>")
	if len(sections) != 4 {
		t.Fatalf("want a header and three sections, got %d", len(sections))
	}
	open, pending, closed := sections[1], sections[2], sections[3]
	if !strings.Contains(open, "Open positions") || !strings.Contains(open, "TSLL") || strings.Contains(open, "MARA") || strings.Contains(open, "PDD") {
		t.Errorf("open section wrong:\n%s", open)
	}
	if !strings.Contains(pending, "PENDING_FILL") || !strings.Contains(pending, "MARA") || !strings.Contains(pending, "provisional") {
		t.Errorf("pending section wrong:\n%s", pending)
	}
	if !strings.Contains(closed, "CLOSED_PROFIT") || !strings.Contains(closed, "PDD") || !strings.Contains(closed, "PROFIT_TAKER_FILLED") {
		t.Errorf("closed section wrong:\n%s", closed)
	}
	for _, col := range []string{"exit_date", "exit_date_source", "entry_filled_at", "trading_days_held", "entry_order_id", "pnl_pct"} {
		if !strings.Contains(open, "<th>"+col+"</th>") {
			t.Errorf("open table missing column %s", col)
		}
	}
	// The TSLL trade filled Thursday with a 1-day window: due Friday, from the fill.
	if !strings.Contains(open, "2026-09-25") || !strings.Contains(open, "fill") {
		t.Errorf("exit date 2026-09-25 (fill) not shown:\n%s", open)
	}
	// Reading a page writes nothing.
	var n int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM manage_exit`).Scan(&n); err != nil || n != 3 {
		t.Errorf("rows after viewing = %d (err %v), want the same 3", n, err)
	}
}

func TestManageExitPageEscapesAndIsGETOnly(t *testing.T) {
	srv, h := checkHealthServer(t)
	now := time.Now().UTC()
	if _, err := manage_exits.Open(srv.db, manage_exits.OpenTradeParams{AccountNumber: "1", Symbol: `<script>alert(1)</script>`, StrategyName: "s",
		EntryOrderID: "9", EntryPrice: 1, TargetProfitPrice: 2, Quantity: 1, EntryTime: now, FilledAt: now, MaxTradingDays: 1}); err != nil {
		t.Fatal(err)
	}
	if body := getManageExit(h, "GET", "admin", "s3cr3t").Body.String(); strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("cell text must be HTML-escaped")
	}
	if w := getManageExit(h, "POST", "admin", "s3cr3t"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", w.Code)
	}
}

// exit_date is what an operator reads this page for: it must be visible without scrolling
// past the prices, so it sits right after status.
func TestManageExitPageShowsExitDateRightAfterStatus(t *testing.T) {
	srv, h := checkHealthServer(t)
	now := time.Now().UTC()
	if _, err := manage_exits.Open(srv.db, manage_exits.OpenTradeParams{AccountNumber: "1", Symbol: "TSLL", StrategyName: "s",
		EntryOrderID: "1", EntryPrice: 10, TargetProfitPrice: 11, Quantity: 1, EntryTime: now, FilledAt: now, MaxTradingDays: 1}); err != nil {
		t.Fatal(err)
	}
	body := getManageExit(h, "GET", "admin", "s3cr3t").Body.String()
	status, exit, src, qty := strings.Index(body, "<th>status</th>"), strings.Index(body, "<th>exit_date</th>"),
		strings.Index(body, "<th>exit_date_source</th>"), strings.Index(body, "<th>quantity</th>")
	if status < 0 || exit < 0 || src < 0 || qty < 0 {
		t.Fatalf("columns missing: status=%d exit_date=%d source=%d quantity=%d", status, exit, src, qty)
	}
	if !(status < exit && exit < src && src < qty) {
		t.Errorf("column order wrong: status=%d exit_date=%d exit_date_source=%d quantity=%d", status, exit, src, qty)
	}
}

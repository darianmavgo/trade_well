package accountstore

import (
	"database/sql"
	"testing"
	"time"
)

func exitsDB(t *testing.T, positions string) (*Store, *sql.DB) {
	t.Helper()
	db := openTest(t)
	s := New(Default)
	if err := s.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	s.ParseAccounts(db, []byte(positions), time.Now().UTC())
	return s, db
}

func exitsOf(t *testing.T, db *sql.DB, acc, sym string) (pt, sl sql.NullFloat64) {
	t.Helper()
	if err := db.QueryRow(`SELECT profit_taker, stop_loss FROM positions WHERE account_number = ? AND symbol = ?`, acc, sym).Scan(&pt, &sl); err != nil {
		t.Fatalf("%s %s: %v", acc, sym, err)
	}
	return pt, sl
}

func want(t *testing.T, name string, got sql.NullFloat64, exp float64) {
	t.Helper()
	if !got.Valid || got.Float64 != exp {
		t.Errorf("%s = %+v, want %v", name, got, exp)
	}
}

func none(t *testing.T, name string, got sql.NullFloat64) {
	t.Helper()
	if got.Valid {
		t.Errorf("%s = %v, want NULL: Schwab shows no such order, so nothing is invented", name, got.Float64)
	}
}

const twoAccounts = `[
 {"securitiesAccount":{"accountNumber":"111","type":"CASH","positions":[
   {"instrument":{"symbol":"TSLL","assetType":"EQUITY"},"longQuantity":1,"averagePrice":10.30,"marketValue":10.2},
   {"instrument":{"symbol":"MARA","assetType":"EQUITY"},"longQuantity":113,"averagePrice":13.35,"marketValue":1500}]}},
 {"securitiesAccount":{"accountNumber":"222","type":"CASH","positions":[
   {"instrument":{"symbol":"TSLL","assetType":"EQUITY"},"longQuantity":5,"averagePrice":10.30,"marketValue":51}]}}]`

// The layout Schwab returns for a filled bracket: the FILLED entry carries the live OCO
// exits as its children.
const filledBracket = `[{"orderId":1001,"status":"FILLED","orderType":"LIMIT","price":10.30,"orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"legId":1,"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL","assetType":"EQUITY"}}],
 "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":1002,"status":"WORKING","orderType":"LIMIT","price":10.82,"orderLegCollection":[{"legId":1,"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL","assetType":"EQUITY"}}]},
   {"orderId":1003,"status":"WORKING","orderType":"STOP","stopPrice":8.24,"orderLegCollection":[{"legId":1,"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL","assetType":"EQUITY"}}]}]}]}]`

func TestPositionsCarryTheTakeProfitAndStopSchwabIsActuallyHolding(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	s.ParseOrders(db, "111", []byte(filledBracket), time.Now().UTC())

	pt, sl := exitsOf(t, db, "111", "TSLL")
	want(t, "profit_taker", pt, 10.82)
	want(t, "stop_loss", sl, 8.24)

	// A position with no resting exit is NULL, not a number worked out from its cost.
	pt, sl = exitsOf(t, db, "111", "MARA")
	none(t, "MARA profit_taker", pt)
	none(t, "MARA stop_loss", sl)
}

// Only working SELL limit/stop orders count: not history, not buys, not exits of an
// entry that has not filled, not short sales.
func TestOnlyWorkingSellExitsCount(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	orders := `[
	 {"orderId":1,"status":"CANCELED","orderType":"LIMIT","price":99,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":2,"status":"FILLED","orderType":"LIMIT","price":98,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":3,"status":"WORKING","orderType":"LIMIT","price":97,"orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":4,"status":"WORKING","orderType":"STOP","stopPrice":1.11,"orderLegCollection":[{"instruction":"SELL_SHORT","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":5,"status":"WORKING","orderType":"TRIGGER","orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"TSLL"}}],
	  "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
	    {"orderId":6,"status":"AWAITING_PARENT_ORDER","orderType":"LIMIT","price":55,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	    {"orderId":7,"status":"AWAITING_PARENT_ORDER","orderType":"STOP","stopPrice":44,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]}]}]}]`
	s.ParseOrders(db, "111", []byte(orders), time.Now().UTC())
	pt, sl := exitsOf(t, db, "111", "TSLL")
	none(t, "profit_taker", pt)
	none(t, "stop_loss", sl)
}

// Two working exits on one symbol: the take-profit that fills first (lowest limit) and the
// stop that triggers first (highest stop). A STOP_LIMIT counts by its trigger price.
func TestNearestExitWinsAndStopLimitUsesItsTrigger(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	orders := `[
	 {"orderId":1,"status":"WORKING","orderType":"LIMIT","price":11.50,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":2,"status":"WORKING","orderType":"LIMIT","price":10.82,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":3,"status":"WORKING","orderType":"STOP","stopPrice":8.24,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	 {"orderId":4,"status":"QUEUED","orderType":"STOP_LIMIT","stopPrice":9.00,"price":8.90,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]}]`
	s.ParseOrders(db, "111", []byte(orders), time.Now().UTC())
	pt, sl := exitsOf(t, db, "111", "TSLL")
	want(t, "profit_taker", pt, 10.82)
	want(t, "stop_loss", sl, 9.00)
}

// An exit cancelled since the last sync disappears; another account is not touched.
func TestACancelledExitDisappearsAndOtherAccountsAreLeftAlone(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	now := time.Now().UTC()
	s.ParseOrders(db, "111", []byte(filledBracket), now)
	s.ParseOrders(db, "222", []byte(`[{"orderId":2001,"status":"WORKING","orderType":"LIMIT","price":12.34,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]}]`), now)

	pt, _ := exitsOf(t, db, "222", "TSLL")
	want(t, "222 profit_taker", pt, 12.34)

	// Next sync for account 111: both exit legs are gone (cancelled).
	s.ParseOrders(db, "111", []byte(`[{"orderId":1001,"status":"FILLED","childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
	  {"orderId":1002,"status":"CANCELED","orderType":"LIMIT","price":10.82,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
	  {"orderId":1003,"status":"CANCELED","orderType":"STOP","stopPrice":8.24,"orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]}]}]}]`), now)
	pt, sl := exitsOf(t, db, "111", "TSLL")
	none(t, "111 profit_taker after cancel", pt)
	none(t, "111 stop_loss after cancel", sl)
	pt, _ = exitsOf(t, db, "222", "TSLL")
	want(t, "222 profit_taker is untouched", pt, 12.34)
}

// A positions table created before these columns existed keeps its rows and gains them.
func TestEnsureSchemaUpgradesAnOldPositionsTable(t *testing.T) {
	db := openTest(t)
	if _, err := db.Exec(`CREATE TABLE positions (
		id INTEGER PRIMARY KEY AUTOINCREMENT, account_number TEXT NOT NULL, symbol TEXT NOT NULL, asset_type TEXT, cusip TEXT,
		long_quantity REAL, short_quantity REAL, average_price REAL, market_value REAL, current_day_pl REAL, current_day_pl_pct REAL,
		settled_long_qty REAL, settled_short_qty REAL, raw_json TEXT, fetched_at DATETIME NOT NULL, UNIQUE(account_number, symbol, asset_type));
		INSERT INTO positions (account_number, symbol, asset_type, long_quantity, fetched_at) VALUES ('111','TSLL','EQUITY',1,'2026-09-23 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	s := New(Default)
	if err := s.EnsureSchema(db); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := s.EnsureSchema(db); err != nil { // and again
		t.Fatal(err)
	}
	pt, sl := exitsOf(t, db, "111", "TSLL")
	none(t, "old row profit_taker", pt)
	none(t, "old row stop_loss", sl)
	var exit sql.NullString
	if err := db.QueryRow(`SELECT exit_date FROM positions WHERE symbol = 'TSLL'`).Scan(&exit); err != nil || exit.Valid {
		t.Errorf("an old row must gain a NULL exit_date column: %+v (err %v)", exit, err)
	}
	s.ParseOrders(db, "111", []byte(filledBracket), time.Now().UTC())
	pt, sl = exitsOf(t, db, "111", "TSLL")
	want(t, "profit_taker", pt, 10.82)
	want(t, "stop_loss", sl, 8.24)
}

func TestPositionExitsTableShowsNoneWhereSchwabHoldsNoExit(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	s.ParseOrders(db, "111", []byte(filledBracket), time.Now().UTC())
	tbl, err := s.PositionExitsTable(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, r := range tbl.Rows {
		got[r[0]+"/"+r[1]] = [2]string{r[8], r[9]}
		if r[10] != "none" {
			t.Errorf("%s/%s exit_date = %q, want none: no date was supplied", r[0], r[1], r[10])
		}
	}
	if got["111/TSLL"] != [2]string{"10.82", "8.24"} || got["111/MARA"] != [2]string{"none", "none"} || got["222/TSLL"] != [2]string{"none", "none"} {
		t.Errorf("table = %v", got)
	}
	if tbl.Columns[8] != "profit_taker" || tbl.Columns[9] != "stop_loss" || tbl.Columns[10] != "exit_date" || tbl.Columns[0] != "account_number" || tbl.Columns[7] != "current_day_pl" {
		t.Errorf("columns = %v: the exits go after the columns the page always showed", tbl.Columns)
	}
	if tbl.Name != Default.Position || tbl.Label != "Positions" {
		t.Errorf("name/label = %q/%q", tbl.Name, tbl.Label)
	}
}

// Building a display table must not need a database.
func TestParseOrdersWithDiscardStillWorks(t *testing.T) {
	s := New(Default)
	tbl := s.ParseOrders(Discard, "111", []byte(filledBracket), time.Now().UTC())
	if len(tbl.Rows) != 1 {
		t.Errorf("rows = %v", tbl.Rows)
	}
}

// The planned exit date is supplied by the caller (it comes from a strategy's hold window,
// which the broker knows nothing about); the store only holds it, per account, and clears
// what is no longer planned.
func TestPlannedExitDatesAreSuppliedAndClearedPerAccount(t *testing.T) {
	s, db := exitsDB(t, twoAccounts)
	dates := map[string]map[string]string{
		"111": {"TSLL": "2026-09-25", "GONE": "2026-09-30", "MARA": ""},
		"222": {"TSLL": "2026-10-02"},
	}
	s.SetPositionExitDates(db, []string{"111", "222"}, dates)
	get := func(acc, sym string) sql.NullString {
		var d sql.NullString
		if err := db.QueryRow(`SELECT exit_date FROM positions WHERE account_number = ? AND symbol = ?`, acc, sym).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := get("111", "TSLL"); d.String != "2026-09-25" {
		t.Errorf("111 TSLL = %+v", d)
	}
	if d := get("222", "TSLL"); d.String != "2026-10-02" {
		t.Errorf("222 TSLL = %+v", d)
	}
	if d := get("111", "MARA"); d.Valid {
		t.Errorf("an empty date must leave the position without one, got %+v", d)
	}

	// The next sync only knows account 111's TSLL trade closed: its date goes, 222 is not in scope.
	s.SetPositionExitDates(db, []string{"111"}, map[string]map[string]string{})
	if d := get("111", "TSLL"); d.Valid {
		t.Errorf("a date whose trade closed must disappear, got %+v", d)
	}
	if d := get("222", "TSLL"); d.String != "2026-10-02" {
		t.Errorf("an account outside the scope must be untouched, got %+v", d)
	}

	// Shown in the table, after the two prices.
	s.SetPositionExitDates(db, []string{"111"}, map[string]map[string]string{"111": {"TSLL": "2026-09-25"}})
	tbl, _ := s.PositionExitsTable(db)
	for _, r := range tbl.Rows {
		if r[0] == "111" && r[1] == "TSLL" && r[10] != "2026-09-25" {
			t.Errorf("table exit_date = %q", r[10])
		}
	}
}

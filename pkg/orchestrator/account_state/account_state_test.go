package account_state

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	return db
}

func TestResetRawTablesOnEmptyDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := resetRawTables(db); err != nil {
		t.Fatalf("reset on empty db: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM account_state_raw_response").Scan(&n); err != nil {
		t.Fatalf("account_state_raw_response missing after reset: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected empty account_state_raw_response, got %d", n)
	}
}

func TestMergedAccountsTableHasMultipleRows(t *testing.T) {
	a, _ := accountTablesFromJSON([]byte(`{"securitiesAccount":{"accountNumber":"25474194","type":"MARGIN","currentBalances":{"cashBalance":1,"availableFunds":1,"buyingPower":1,"liquidationValue":1}}}`))
	b, _ := accountTablesFromJSON([]byte(`{"securitiesAccount":{"accountNumber":74234603,"type":"MARGIN","currentBalances":{"cashBalance":2,"availableFunds":2,"buyingPower":2,"liquidationValue":2}}}`))
	merged := &Table{Name: "account_state_account", Label: "Accounts", Columns: a.Columns}
	merged.Rows = append(merged.Rows, a.Rows...)
	merged.Rows = append(merged.Rows, b.Rows...)
	if len(merged.Rows) != 2 || merged.Rows[0][0] != "25474194" || merged.Rows[1][0] != "74234603" {
		t.Fatalf("expected two account rows, got %+v", merged.Rows)
	}
}

func TestAccountTablesFromJSONDoesNotNeedDB(t *testing.T) {
	payload := []byte(`{"securitiesAccount":{"accountNumber":74234603,"type":"MARGIN","currentBalances":{"cashBalance":10,"availableFunds":10,"buyingPower":10,"liquidationValue":10},"positions":[{"instrument":{"symbol":"VOO","assetType":"EQUITY"},"longQuantity":1,"averagePrice":400,"marketValue":400}]}}`)
	acc, pos := accountTablesFromJSON(payload)
	if acc == nil || len(acc.Rows) != 1 || acc.Rows[0][0] != "74234603" {
		t.Fatalf("expected account 74234603, got %+v", acc)
	}
	if pos == nil || len(pos.Rows) != 1 || pos.Rows[0][1] != "VOO" {
		t.Fatalf("expected VOO position, got %+v", pos)
	}
}

func TestParseAndSaveOrdersIncludesTicker(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	payload := []byte(`[
		{
			"orderId": 123,
			"accountNumber": "25474194",
			"orderType": "LIMIT",
			"status": "WORKING",
			"price": 90.5,
			"quantity": 3,
			"filledQuantity": 0,
			"enteredTime": "2026-09-16T13:00:00Z",
			"orderLegCollection": [
				{"legId": 1, "instruction": "BUY", "quantity": 3, "instrument": {"symbol": "TECL", "assetType": "EQUITY"}}
			]
		}
	]`)
	tbl := store.ParseOrders(db, "25474194", payload, time.Now().UTC())
	if tbl == nil || len(tbl.Rows) != 1 {
		t.Fatalf("expected 1 order row, got %+v", tbl)
	}
	if tbl.Columns[0] != "symbol" || tbl.Rows[0][0] != "TECL" {
		t.Fatalf("expected ticker TECL in first column, columns=%v row=%v", tbl.Columns, tbl.Rows[0])
	}
	if tbl.Rows[0][1] != "BUY" {
		t.Fatalf("expected instruction BUY, got %q", tbl.Rows[0][1])
	}
}

// TestParseAndSaveOrdersRendersFullOrderID reproduces the /account_state bug: a
// as the full integer, in both the displayed table row and the order_id
// column actually written to account_state_order -- not "1.007941386747e+12".
func TestParseAndSaveOrdersRendersFullOrderID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	payload := []byte(`[
		{
			"orderId": 1007941386747,
			"accountNumber": 25474194,
			"orderType": "LIMIT",
			"status": "FILLED",
			"price": 360,
			"quantity": 31,
			"filledQuantity": 31,
			"enteredTime": "2026-09-16T13:48:13Z",
			"orderLegCollection": [
				{"legId": 1, "instruction": "SELL", "quantity": 31, "instrument": {"symbol": "TSLA", "assetType": "EQUITY"}}
			]
		}
	]`)
	tbl := store.ParseOrders(db, "25474194", payload, time.Now().UTC())
	if tbl == nil || len(tbl.Rows) != 1 {
		t.Fatalf("expected 1 order row, got %+v", tbl)
	}
	orderIDCol := -1
	for i, c := range tbl.Columns {
		if c == "order_id" {
			orderIDCol = i
		}
	}
	if orderIDCol < 0 || tbl.Rows[0][orderIDCol] != "1007941386747" {
		t.Fatalf("expected the full order id, got %q (columns=%v)", tbl.Rows[0][orderIDCol], tbl.Columns)
	}

	var stored string
	if err := db.QueryRow("SELECT order_id FROM account_state_order WHERE account_number = '25474194'").Scan(&stored); err != nil {
		t.Fatalf("failed to read back stored order_id: %v", err)
	}
	if stored != "1007941386747" {
		t.Fatalf("expected the full order id stored as the primary key, got %q", stored)
	}
}

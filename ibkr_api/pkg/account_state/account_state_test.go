package account_state

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	_ "modernc.org/sqlite"
)

// The IBKR positions payload (as GetAccount maps it) must land in
// account_state_position exactly as a Schwab account would.
func TestMappedIBKRAccountLandsInAccountStatePosition(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	sec, _ := json.Marshal(map[string]any{
		"type": "IBKR", "accountNumber": "U1234567",
		"currentBalances": map[string]float64{"cashBalance": 100, "liquidationValue": 1500, "buyingPower": 200, "cashAvailableForTrading": 100},
		"positions": []ibkr.SchwabPosition{{LongQuantity: 3, AveragePrice: 10.5, MarketValue: 33,
			Instrument: map[string]any{"symbol": "TSLL", "assetType": "EQUITY"}}},
	})
	wrapped, _ := json.Marshal(map[string]json.RawMessage{"securitiesAccount": sec})
	_, pos := store.ParseAccounts(db, wrapped, time.Now())
	if pos == nil || len(pos.Rows) != 1 {
		t.Fatalf("positions table = %+v", pos)
	}
	var sym string
	var qty float64
	if err := db.QueryRow(`SELECT symbol, long_quantity FROM account_state_position`).Scan(&sym, &qty); err != nil || sym != "TSLL" || qty != 3 {
		t.Errorf("row = %q %v err=%v", sym, qty, err)
	}
}

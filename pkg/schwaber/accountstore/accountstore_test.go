package accountstore

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestIngestWithMirrorAndCustomTables(t *testing.T) {
	custom := Tables{Raw: "p_raw", Account: "p_acct", Position: "p_pos", Order: "p_ord", OrderLeg: "p_leg", Transaction: "p_tx", Preference: "p_pref"}
	for name, tbl := range map[string]Tables{"mirror": Mirror, "custom": custom} {
		t.Run(name, func(t *testing.T) {
			db := openTest(t)
			s := New(tbl)
			if err := s.EnsureSchema(db); err != nil {
				t.Fatal(err)
			}
			if err := s.EnsureSchema(db); err != nil { // idempotent
				t.Fatal(err)
			}
			now := time.Now().UTC()

			acc, pos := s.ParseAccounts(db, []byte(`[{"securitiesAccount":{"accountNumber":"111","type":"CASH",
				"currentBalances":{"cashBalance":10.5,"liquidationValue":99},
				"positions":[{"instrument":{"symbol":"TECL","assetType":"EQUITY"},"longQuantity":3,"marketValue":30}]}}]`), now)
			if len(acc.Rows) != 1 || len(pos.Rows) != 1 {
				t.Fatalf("display rows acc=%d pos=%d", len(acc.Rows), len(pos.Rows))
			}
			if acc.Name != tbl.Account || pos.Name != tbl.Position {
				t.Fatalf("table names %q %q", acc.Name, pos.Name)
			}

			// 13-digit order id must not be rendered in scientific notation.
			ord := s.ParseOrders(db, "111", []byte(`[{"orderId":1007974038446,"status":"FILLED","orderLegCollection":[{"legId":1,"instruction":"BUY","quantity":3,"instrument":{"symbol":"TECL"}}]}]`), now)
			if len(ord.Rows) != 1 || ord.Rows[0][2] != "1007974038446" {
				t.Fatalf("order rows = %v", ord.Rows)
			}
			tx := s.ParseTransactions(db, "111", []byte(`[{"activityId":55,"type":"TRADE","netAmount":-5}]`), now)
			if len(tx.Rows) != 1 {
				t.Fatalf("tx rows = %v", tx.Rows)
			}
			s.ParseUserPreferences(db, []byte(`{"streamerInfo":[{"streamerSocketUrl":"wss://x"}]}`), now)
			s.SaveRawResponse(db, "/accounts", "GET", 200, "111", []byte(`[]`), now)

			rep, err := s.Summary(db)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, c := range rep.TableCounts {
				counts[c.Label] = c.Count
			}
			for label, want := range map[string]int{"Accounts": 1, "Open Positions": 1, "Recent Orders": 1, "Order Legs": 1, "Transactions": 1, "User Preferences": 1} {
				if counts[label] != want {
					t.Errorf("%s = %d, want %d (%v)", label, counts[label], want, counts)
				}
			}
			if len(rep.Accounts) != 1 || rep.Accounts[0].CashBalance != 10.5 {
				t.Fatalf("accounts = %+v", rep.Accounts)
			}
		})
	}
}

func TestDiscardBuildsTablesWithoutDB(t *testing.T) {
	s := New(Mirror)
	tbl := s.ParseTransactions(Discard, "1", []byte(`[{"activityId":"a","netAmount":1.5}]`), time.Time{})
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %v", tbl.Rows)
	}
}

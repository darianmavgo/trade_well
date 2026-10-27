package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func tableRows(t *testing.T, srv *Server, url, name string) int {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleTables(w, httptest.NewRequest("GET", url, nil))
	var resp struct {
		Tables []TableDump `json:"tables"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Tables {
		if d.Name == name {
			if d.Error != "" {
				t.Fatalf("%s: %s", name, d.Error)
			}
			return len(d.Rows)
		}
	}
	t.Fatalf("no table %s", name)
	return 0
}

// The default view shows what is live; ?all=1 shows everything, retired rows and old runs too.
func TestTablesDefaultToCurrentRowsAndAllShowsHistory(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "live_a+live_b,solo")
	db := setupTestDB(t)
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	order := func(strat, status string) {
		exec(`INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, staged_at)
			VALUES ('1', ?, 'X', 1, 10, 11, 9, ?, datetime('now'))`, strat, status)
	}
	order("live_b", "REJECTED") // member of a greenlit stack: shown
	order("solo", "PENDING")    // greenlit on its own: shown
	order("retired", "PENDING") // not greenlit: hidden
	order("live_a", "CLEARED")  // cleared by clear_stale: hidden
	trade := func(strat, code string) {
		exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, entry_price, target_profit_price, quantity, entry_time, status, exit_reason_code)
			VALUES ('1', 'X', ?, 10, 11, 1, datetime('now'), 'OPEN', ?)`, strat, code)
	}
	trade("live_a", "")
	trade("live_a", "STALE_CLEARED")
	trade("retired", "")
	for _, run := range []string{"2026-10-13T14:00:02Z", "2026-10-14T14:00:02Z", "2026-10-14T14:00:03Z"} {
		exec(`INSERT INTO check_health_finding (run_at, account_number, check_name, category, severity, subject, detail) VALUES (?, '1', 'c', 'cat', 'WARNING', 's', 'd')`, run)
	}

	srv := &Server{dbPath: ":memory:", port: 8080, db: db}
	for _, c := range []struct {
		table        string
		current, all int
	}{
		{"stage_order", 2, 4},
		{"manage_exit", 1, 3},
		{"check_health_finding", 2, 3}, // only the latest run's hour
	} {
		if got := tableRows(t, srv, "/api/tables", c.table); got != c.current {
			t.Errorf("%s default view = %d rows, want %d", c.table, got, c.current)
		}
		if got := tableRows(t, srv, "/api/tables?all=1", c.table); got != c.all {
			t.Errorf("%s ?all=1 = %d rows, want %d", c.table, got, c.all)
		}
	}
}

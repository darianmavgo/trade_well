package trigger_limits

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func TestSeatPlacesNothingWhileRiskHalted(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := stage_orders.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := risk.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons) VALUES ('111',?,?,?,?,?,?,?,?,1,1.0,'daily loss')`,
		time.Now().UTC(), "2026-10-15", 800.0, 1000.0, 0.2, -0.06, 0.3, 0.0); err != nil {
		t.Fatal(err)
	}
	// DryRun with AnySession reaches the risk gate with no broker client and no quote source,
	// which a halted account must never get as far as asking for.
	res, err := Seat(context.Background(), db, nil, &trader.ResolvedAccount{AccountNumber: "111"}, Options{DryRun: true, AnySession: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Seated != 0 || len(res.Details) != 1 || !strings.Contains(res.Details[0], "no new limits seated") {
		t.Fatalf("result %+v", res)
	}
}

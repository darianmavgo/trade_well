package submit_orders

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func stageBuy(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber: "74234603", StrategyName: "s1", Symbol: "VOO", Instruction: trader.InstructionBuy,
		Quantity: 3, EntryPrice: 500, TakeProfitPrice: 525, StopLossPrice: 475, MaxTradingDays: 8,
		Status: stage_orders.StagedOrderPending, StagedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func halt(t *testing.T, db *sql.DB, haltEntries int) {
	t.Helper()
	if err := risk.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons)
VALUES ('74234603',?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now().UTC(), "2026-10-15", 800.0, 1000.0, 0.2, -0.01, 0.3, 0.0, haltEntries, 1.0, "drawdown 20.0% is past the 20% step"); err != nil {
		t.Fatal(err)
	}
}

func TestRiskHaltRejectsStagedBuy(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	stageBuy(t, db)
	halt(t, db, 1)

	res, err := Run(context.Background(), db, nil, &trader.ResolvedAccount{AccountNumber: "74234603"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "RISK_HALTED" || res.Rejected != 1 {
		t.Fatalf("rows %+v rejected %d", res.Rows, res.Rejected)
	}
	if pending, _ := stage_orders.GetPendingStagedOrders(db, "74234603"); len(pending) != 0 {
		t.Fatalf("a halted buy must leave the queue, %d still pending", len(pending))
	}
}

func TestRiskHaltDryRunLeavesOrderPending(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	stageBuy(t, db)
	halt(t, db, 1)

	res, err := Run(context.Background(), db, nil, &trader.ResolvedAccount{AccountNumber: "74234603"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "RISK_HALTED" {
		t.Fatalf("rows %+v", res.Rows)
	}
	if pending, _ := stage_orders.GetPendingStagedOrders(db, "74234603"); len(pending) != 1 {
		t.Fatalf("a dry run must not reject, %d pending", len(pending))
	}
}

func TestReducedSizeStateDoesNotBlockSubmission(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	stageBuy(t, db)
	halt(t, db, 0)

	res, err := Run(context.Background(), db, nil, &trader.ResolvedAccount{AccountNumber: "74234603"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "PENDING" {
		t.Fatalf("not halted, no client: want PENDING, got %+v", res.Rows)
	}
}

// A live run that cannot read the risk state places nothing and leaves the order queued for the
// next run; a dry run only previews. The unreadable state here is a risk_state table of an older,
// narrower shape, which is what a bad restore would look like.
func TestUnreadableRiskStateFailsClosed(t *testing.T) {
	for _, dry := range []bool{false, true} {
		db := setupTestDB(t)
		stageBuy(t, db)
		if _, err := db.Exec(`CREATE TABLE risk_state (account_number TEXT PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		res, err := Run(context.Background(), db, nil, &trader.ResolvedAccount{AccountNumber: "74234603"}, dry)
		if err != nil {
			t.Fatal(err)
		}
		if dry {
			if len(res.Rows) != 1 || res.Rows[0].Status == "RISK_UNREADABLE" {
				t.Fatalf("dry run must not block on unreadable risk state: %+v", res.Rows)
			}
		} else {
			if len(res.Rows) != 1 || res.Rows[0].Status != "RISK_UNREADABLE" {
				t.Fatalf("live run must fail closed: %+v", res.Rows)
			}
			if pending, _ := stage_orders.GetPendingStagedOrders(db, "74234603"); len(pending) != 1 {
				t.Fatalf("the order must stay queued, %d pending", len(pending))
			}
		}
		db.Close()
	}
}

func TestOrderCapRejectsOversizedBuy(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	stageBuy(t, db) // 3 x $500 = $1,500
	halt(t, db, 0)  // equity 800 in the state; the live account below reports $2,000
	if _, err := db.Exec(`UPDATE risk_state SET max_order_pct = 0.5`); err != nil {
		t.Fatal(err)
	}
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	acc.Details.CurrentBalances.LiquidationValue = 2000 // $1,500 is 75% of equity, over the 50% cap

	res, err := Run(context.Background(), db, nil, acc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "RISK_LIMIT" || res.Rejected != 1 {
		t.Fatalf("rows %+v rejected %d", res.Rows, res.Rejected)
	}
}

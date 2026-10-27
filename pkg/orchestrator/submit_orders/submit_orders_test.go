package submit_orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	for _, ensure := range []func(*sql.DB) error{EnsureSchema, stage_orders.EnsureSchema, manage_exits.EnsureSchema, account_state.EnsureSchema} {
		if err := ensure(db); err != nil {
			t.Fatalf("failed to init schema: %v", err)
		}
	}
	return db
}

func TestCheckOrderFill_NoTraderClient(t *testing.T) {
	if got, _ := checkOrderFill(context.Background(), nil, time.Now().UTC(), time.Time{}, "12345"); got != "" {
		t.Errorf("expected empty status with a nil trader client, got %q", got)
	}
	if got, _ := checkOrderFill(context.Background(), nil, time.Now().UTC(), time.Time{}, ""); got != "" {
		t.Errorf("expected empty status for an empty order id, got %q", got)
	}
}

// TestRunOrderSubmission_PendingFillWithoutClientStaysUnresolved reproduces
// the "accepted but not yet confirmed filled" limbo state on a run with no
// live trader client (e.g. a stale/missing token) -- the order must stay
// SUBMITTED_PENDING_FILL, and no manage_exit row may be fabricated for it.
func TestRunOrderSubmission_PendingFillWithoutClientStaysUnresolved(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "mara_tree",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        133,
		EntryPrice:      11.24,
		TakeProfitPrice: 11.80,
		StopLossPrice:   10.34,
		MaxTradingDays:  1,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder error: %v", err)
	}
	if err := stage_orders.MarkStagedOrderPendingFill(db, id, "1007974038446", time.Now().UTC()); err != nil {
		t.Fatalf("MarkStagedOrderPendingFill error: %v", err)
	}

	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	res, err := Run(context.Background(), db, nil, acc, false)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.StagedOrdersFound != 1 {
		t.Fatalf("expected the fill-pending order to be counted, got %d", res.StagedOrdersFound)
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "SUBMITTED_PENDING_FILL" {
		t.Fatalf("expected the order to remain SUBMITTED_PENDING_FILL, got %+v", res.Rows)
	}

	stillPending, err := stage_orders.GetOrdersAwaitingFillConfirmation(db, "74234603")
	if err != nil {
		t.Fatalf("GetOrdersAwaitingFillConfirmation error: %v", err)
	}
	if len(stillPending) != 1 {
		t.Fatalf("expected the order to still be awaiting fill confirmation, got %d", len(stillPending))
	}

	open, err := manage_exits.GetOpenTrades(db, "74234603")
	if err != nil {
		t.Fatalf("GetOpenTrades error: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("expected NO manage_exit row for an unconfirmed fill, got %+v", open)
	}
}

func TestRunOrderSubmissionWithoutTraderClientLeavesOrdersPending(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "sig-voo-buy-tecl",
		Symbol:          "VOO",
		Instruction:     trader.InstructionBuy,
		Quantity:        3,
		EntryPrice:      500.0,
		TakeProfitPrice: 525.0,
		StopLossPrice:   475.0,
		MaxTradingDays:  8,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("failed to insert staged order: %v", err)
	}

	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}

	res, err := Run(context.Background(), db, nil, acc, false)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.StagedOrdersFound != 1 || res.Submitted != 0 {
		t.Errorf("expected 1 staged order found and 0 submitted without a trader client, got %+v", res)
	}

	pending, err := stage_orders.GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get pending staged orders: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected the staged order to remain PENDING without a trader client, got %d pending", len(pending))
	}
	if len(res.Rows) != 1 || res.Rows[0].Status != "PENDING" || res.Rows[0].Strategy != "sig-voo-buy-tecl" {
		t.Errorf("expected 1 PENDING email row, got %+v", res.Rows)
	}
}

// TestRunOrderSubmissionRejectsDuplicatePendingOrders reproduces the
// production bug: two PENDING BUY rows staged for the same account+symbol
// (e.g. scan_market re-run before submit_orders drained the queue). Only
// the oldest-staged row should be attempted; the newer duplicate must be
// rejected outright rather than risk a second live order for the same
// position.
func TestRunOrderSubmissionRejectsDuplicatePendingOrders(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	older := time.Now().UTC().Add(-1 * time.Hour)
	newer := time.Now().UTC()

	_, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "mara_tree",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        1623,
		EntryPrice:      11.24,
		TakeProfitPrice: 11.80,
		StopLossPrice:   10.34,
		MaxTradingDays:  1,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        older,
	})
	if err != nil {
		t.Fatalf("failed to insert first staged order: %v", err)
	}
	_, err = stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "mara_tree",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        1624,
		EntryPrice:      11.24,
		TakeProfitPrice: 11.80,
		StopLossPrice:   10.34,
		MaxTradingDays:  1,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        newer,
	})
	if err != nil {
		t.Fatalf("failed to insert duplicate staged order: %v", err)
	}

	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	res, err := Run(context.Background(), db, nil, acc, false)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.StagedOrdersFound != 2 {
		t.Fatalf("expected 2 staged orders found, got %d", res.StagedOrdersFound)
	}
	if res.Rejected != 1 {
		t.Errorf("expected the duplicate to be rejected, got Rejected=%d", res.Rejected)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 email rows (one per staged order), got %d: %+v", len(res.Rows), res.Rows)
	}
	var dupRowFound bool
	for _, r := range res.Rows {
		if r.Status == "DUPLICATE_REJECTED" {
			dupRowFound = true
		}
	}
	if !dupRowFound {
		t.Errorf("expected a DUPLICATE_REJECTED row, got %+v", res.Rows)
	}

	pending, err := stage_orders.GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get pending staged orders: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 order to remain PENDING (the oldest), got %d", len(pending))
	}
	if pending[0].Quantity != 1623 {
		t.Errorf("expected the OLDEST staged order to remain PENDING, got qty=%v", pending[0].Quantity)
	}

	all, err := stage_orders.GetStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get all staged orders: %v", err)
	}
	var rejectedFound bool
	for _, o := range all {
		if o.Quantity == 1624 {
			if o.Status != stage_orders.StagedOrderRejected {
				t.Errorf("expected the newer duplicate to be REJECTED, got status=%s", o.Status)
			}
			if o.RejectReason == "" {
				t.Error("expected a reject reason on the duplicate")
			}
			rejectedFound = true
		}
	}
	if !rejectedFound {
		t.Fatal("expected to find the duplicate order in GetStagedOrders")
	}
}

func TestRunOrderSubmissionDryRunDoesNotMutateOnBuildFailure(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// A negative take-profit makes BuildBracketOrderSpec fail (zero is valid: stop-only bracket).
	_, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "sig-voo-buy-tecl",
		Symbol:          "VOO",
		Instruction:     trader.InstructionBuy,
		Quantity:        3,
		EntryPrice:      500.0,
		TakeProfitPrice: -1,
		MaxTradingDays:  8,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("failed to insert staged order: %v", err)
	}

	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}

	res, err := Run(context.Background(), db, nil, acc, true)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Rejected != 1 {
		t.Errorf("expected 1 would-be-rejected order in dry-run, got %d", res.Rejected)
	}

	pending, err := stage_orders.GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get pending staged orders: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected dry-run to leave the staged order PENDING (no DB mutation), got %d pending", len(pending))
	}
}

func TestSubmittingReconcileDoesNotRequireClient(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	id, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber: "74234603", StrategyName: "s", Symbol: "MARA", Instruction: "BUY",
		Quantity: 1, EntryPrice: 10, TakeProfitPrice: 11, StopLossPrice: 9,
		MaxTradingDays: 1, Status: stage_orders.StagedOrderPending, StagedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := stage_orders.ClaimStagedOrderForSubmit(db, id, "co_sub_test")
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	res, err := Run(context.Background(), db, nil, acc, false)
	if err != nil {
		t.Fatal(err)
	}
	// Still SUBMITTING — not blindly re-placed / rejected without client.
	all, _ := stage_orders.GetStagedOrders(db, "74234603")
	if len(all) != 1 || all[0].Status != stage_orders.StagedOrderSubmitting {
		t.Fatalf("expected still SUBMITTING, got %+v details=%v", all, res.Details)
	}
}

func TestEntryTimeUsesBrokerFillTime(t *testing.T) {
	now := time.Date(2026, 9, 25, 13, 28, 0, 0, time.UTC) // next-morning reconcile
	fill := time.Date(2026, 9, 24, 13, 31, 0, 0, time.UTC)
	if got := entryTime(fill, now); !got.Equal(fill) {
		t.Fatalf("entryTime = %v, want the fill time %v", got, fill)
	}
	if got := entryTime(time.Time{}, now); !got.Equal(now) {
		t.Fatalf("no fill time should fall back to now, got %v", got)
	}
	if got := entryTime(now.Add(time.Hour), now); !got.Equal(now) {
		t.Fatalf("a fill time in the future must not be trusted, got %v", got)
	}
}

func TestLookbackStartCoversRestingGTCOrders(t *testing.T) {
	now := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name     string
		placedAt time.Time
		want     time.Time
	}{
		{"unknown placement keeps the 7-day default", time.Time{}, now.Add(-7 * day)},
		{"placed yesterday: still at least 7 days", now.Add(-day), now.Add(-7 * day)},
		{"resting 20 days: reaches back past its placement", now.Add(-20 * day), now.Add(-21 * day)},
		{"resting 90 days: capped inside Schwab's 60-day window", now.Add(-90 * day), now.Add(-58 * day)},
	}
	for _, c := range cases {
		if got := lookbackStart(now, c.placedAt); !got.Equal(c.want) {
			t.Errorf("%s: lookbackStart = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDescribeSubmitFailureExplainsSchwabRefusal(t *testing.T) {
	apiErr := &schwab.APIError{
		Method: "POST", Endpoint: "/trader/v1/accounts/{hash}/orders", Status: 400,
		Body:     `{"message":"A validation error occurred while processing the request."}`,
		CorrelID: "corr-1", RequestJSON: `{"orderType":"LIMIT"}`,
	}
	wrapped := fmt.Errorf("place: %w", apiErr)
	short, full := describeSubmitFailure(wrapped)
	for _, want := range []string{"HTTP 400", "validation error", "schwab_correl_id=corr-1"} {
		if !strings.Contains(short, want) {
			t.Errorf("short missing %q: %s", want, short)
		}
	}
	for _, want := range []string{"correl_id=corr-1", `request={"orderType":"LIMIT"}`, "no field-level reason"} {
		if !strings.Contains(full, want) {
			t.Errorf("full missing %q: %s", want, full)
		}
	}
	if s, f := describeSubmitFailure(errors.New("boom")); s != "boom" || f != "boom" {
		t.Errorf("plain error changed: %q %q", s, f)
	}
}

func TestIsAmbiguousSubmitErrDecidesByStatus(t *testing.T) {
	if isAmbiguousSubmitErr(&schwab.APIError{Status: 400, Body: "unknown timeout eof"}) {
		t.Error("a 400 whose body contains ambiguous-looking words must be a definite refusal")
	}
	if !isAmbiguousSubmitErr(&schwab.APIError{Status: 502}) {
		t.Error("a 502 must stay ambiguous")
	}
	if !isAmbiguousSubmitErr(errors.New("connection reset by peer")) {
		t.Error("non-API errors keep the text matcher")
	}
}

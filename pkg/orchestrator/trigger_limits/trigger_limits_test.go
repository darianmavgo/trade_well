package trigger_limits

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

// book is a limit-entry strategy that takes a name out of the rotation after a fill.
type book struct{}

func (book) ID() string                                                              { return "book" }
func (book) Name() string                                                            { return "book" }
func (book) Description() string                                                     { return "" }
func (book) Validate() error                                                         { return nil }
func (book) SetDatabases(string, string)                                             {}
func (book) GenerateSignals(map[string][]backtestmodels.Bar) []backtestmodels.Signal { return nil }
func (book) DefaultConfig() strategy.StrategyConfig {
	return strategy.StrategyConfig{EntryLimitPct: 0.95, OneFillPerMonth: true, AllocationPct: 0.2}
}

func et(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, job_runs.EasternTime())
}

// Thursday 2026-10-08 is a session.
var session = "2026-10-08"

func setup(t *testing.T) (*sql.DB, *trader.ResolvedAccount) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := stage_orders.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	acc := &trader.ResolvedAccount{AccountNumber: "A1"}
	acc.Details.CurrentBalances.LiquidationValue = 100000
	acc.Details.CurrentBalances.CashAvailableForTrading = 100000
	return db, acc
}

func watch(t *testing.T, db *sql.DB, sym string, limit float64, sessionDate string) int64 {
	t.Helper()
	id, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber: "A1", StrategyName: "book", Symbol: sym, Instruction: trader.InstructionBuy,
		Quantity: 1, EntryPrice: limit, TakeProfitPrice: limit * 1.03, StopLossPrice: limit * 0.95,
		MaxTradingDays: 2, Status: stage_orders.StagedOrderWatching, StagedAt: et(2026, 10, 7, 18, 15).UTC(),
		EntryDuration: stage_orders.EntryDurationDay, SessionDate: sessionDate, AllocPct: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func quotes(m map[string]float64) QuoteFunc {
	return func(_ context.Context, symbols []string) (map[string]Quote, error) {
		out := map[string]Quote{}
		for _, s := range symbols {
			if low, ok := m[s]; ok {
				out[s] = Quote{Low: low, Ask: low + 0.1, Last: low + 0.05}
			}
		}
		return out, nil
	}
}

func status(t *testing.T, db *sql.DB, id int64) (string, float64) {
	t.Helper()
	var st string
	var qty float64
	if err := db.QueryRow(`SELECT status, quantity FROM stage_order WHERE id = ?`, id).Scan(&st, &qty); err != nil {
		t.Fatal(err)
	}
	return st, qty
}

func TestOnlyALimitTheLowReachedIsPlacedAndSizedAgainstTheCashThatIsLeft(t *testing.T) {
	db, acc := setup(t)
	rom := watch(t, db, "ROM", 95, session) // low 94.5: reached
	qld := watch(t, db, "QLD", 50, session) // low 51: not reached
	sso := watch(t, db, "SSO", 80, session) // reached
	uwm := watch(t, db, "UWM", 40, session) // reached
	xpp := watch(t, db, "XPP", 20, session) // reached
	zsl := watch(t, db, "ZSL", 10, session) // reached, but by then the cash is gone
	acc.Details.CurrentBalances.CashAvailableForTrading = 50000

	res, err := Run(context.Background(), db, acc, Options{
		Now: et(2026, 10, 8, 11, 0), Members: map[string]strategy.Strategy{"book": book{}},
		Quotes: quotes(map[string]float64{"ROM": 94.5, "QLD": 51, "SSO": 79, "UWM": 39, "XPP": 19, "ZSL": 9.5}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Watching != 6 || res.Triggered != 5 {
		t.Fatalf("watching %d triggered %d, want 6 and 5: %+v", res.Watching, res.Triggered, res.Details)
	}
	// 20% of 100k is 20k a name. Alphabetical: ROM 210 sh (cash 50k -> 30.05k), SSO 250 sh (-> 10.05k), UWM: cash*0.95 = 9547, qty 238 ...
	if st, qty := status(t, db, rom); st != stage_orders.StagedOrderPending || qty != 210 {
		t.Errorf("ROM %s qty %v, want PENDING 210 (20k at $95)", st, qty)
	}
	if st, qty := status(t, db, sso); st != stage_orders.StagedOrderPending || qty != 250 {
		t.Errorf("SSO %s qty %v, want PENDING 250 (20k at $80)", st, qty)
	}
	if st, _ := status(t, db, qld); st != stage_orders.StagedOrderWatching {
		t.Errorf("QLD (limit not reached) is %s, want it still WATCHING", st)
	}
	if st, qty := status(t, db, uwm); st != stage_orders.StagedOrderPending || qty < 200 || qty > 240 {
		t.Errorf("UWM gets what is left: %s qty %v", st, qty)
	}
	// The cash is spent on the orders that triggered first; later ones are cancelled.
	var cancelled int
	for _, id := range []int64{xpp, zsl} {
		if st, _ := status(t, db, id); st == stage_orders.StagedOrderCancelled {
			cancelled++
		}
	}
	if res.Promoted+res.Cancelled != 5 {
		t.Errorf("every triggered order is either placed or cancelled: placed %d cancelled %d", res.Promoted, res.Cancelled)
	}
	if res.Cancelled != cancelled {
		t.Errorf("cancelled rows %d vs result %d", cancelled, res.Cancelled)
	}
}

func TestNothingHappensBeforeTheOpenOrOnANonSession(t *testing.T) {
	db, acc := setup(t)
	id := watch(t, db, "ROM", 95, session)
	q := quotes(map[string]float64{"ROM": 90})
	for name, now := range map[string]time.Time{
		"before the open":              et(2026, 10, 8, 9, 0),
		"at the open minute minus one": et(2026, 10, 8, 9, 29),
	} {
		res, err := Run(context.Background(), db, acc, Options{Now: now, Quotes: q})
		if err != nil || res.Promoted != 0 || res.Expired != 0 {
			t.Errorf("%s: %+v err %v", name, res, err)
		}
	}
	if st, _ := status(t, db, id); st != stage_orders.StagedOrderWatching {
		t.Fatalf("still watching, got %s", st)
	}
	// A staged order for a session still ahead is left alone, whatever the quote says.
	future := watch(t, db, "SSO", 80, "2026-10-09")
	if _, err := Run(context.Background(), db, acc, Options{Now: et(2026, 10, 8, 11, 0), Quotes: quotes(map[string]float64{"SSO": 70})}); err != nil {
		t.Fatal(err)
	}
	if st, _ := status(t, db, future); st != stage_orders.StagedOrderWatching {
		t.Errorf("an order for tomorrow was %s", st)
	}
}

func TestBuyLimitsExpireAtTheCloseAndAreNeverCarriedOver(t *testing.T) {
	db, acc := setup(t)
	today := watch(t, db, "ROM", 95, session)
	yesterday := watch(t, db, "SSO", 80, "2026-10-07")
	tomorrow := watch(t, db, "UWM", 40, "2026-10-09")
	noQuotes := func(context.Context, []string) (map[string]Quote, error) {
		t.Error("no quote is needed to expire an order")
		return nil, nil
	}
	res, err := Run(context.Background(), db, acc, Options{Now: et(2026, 10, 8, 16, 5), Quotes: noQuotes})
	if err != nil {
		t.Fatal(err)
	}
	if res.Expired != 2 {
		t.Fatalf("expired %d, want today's and yesterday's: %+v", res.Expired, res.Rows)
	}
	for _, id := range []int64{today, yesterday} {
		if st, _ := status(t, db, id); st != stage_orders.StagedOrderCancelled {
			t.Errorf("order %d is %s, want CANCELLED", id, st)
		}
	}
	if st, _ := status(t, db, tomorrow); st != stage_orders.StagedOrderWatching {
		t.Errorf("tomorrow's order is %s", st)
	}
	var reason string
	_ = db.QueryRow(`SELECT reject_reason FROM stage_order WHERE id = ?`, today).Scan(&reason)
	if !strings.Contains(reason, "expired unfilled") {
		t.Errorf("reason %q", reason)
	}
}

func TestATriggeredNameThatFilledEarlierThisMonthOrIsHeldIsNotBought(t *testing.T) {
	db, acc := setup(t)
	rom := watch(t, db, "ROM", 95, session)
	sso := watch(t, db, "SSO", 80, session)
	qld := watch(t, db, "QLD", 50, session)
	filled := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_price, target_profit_price, quantity, entry_time, entry_filled_at, status)
		VALUES ('A1', 'ROM', 'book', 'book', 95, 98, 10, ?, ?, 'CLOSED')`, filled, filled); err != nil {
		t.Fatal(err)
	}
	acc.Details.Positions = []trader.Position{{LongQuantity: 5, Instrument: trader.PositionInstrument{Symbol: "SSO"}}}

	res, err := Run(context.Background(), db, acc, Options{
		Now: et(2026, 10, 8, 12, 0), Members: map[string]strategy.Strategy{"book": book{}},
		Quotes: quotes(map[string]float64{"ROM": 90, "SSO": 70, "QLD": 45}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Promoted != 1 {
		t.Fatalf("placed %d, want only QLD: %+v", res.Promoted, res.Rows)
	}
	for id, want := range map[int64]string{rom: stage_orders.StagedOrderCancelled, sso: stage_orders.StagedOrderCancelled, qld: stage_orders.StagedOrderPending} {
		if st, _ := status(t, db, id); st != want {
			t.Errorf("order %d is %s, want %s", id, st, want)
		}
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	db, acc := setup(t)
	id := watch(t, db, "ROM", 95, session)
	res, err := Run(context.Background(), db, acc, Options{Now: et(2026, 10, 8, 12, 0), DryRun: true, Quotes: quotes(map[string]float64{"ROM": 90})})
	if err != nil || res.Promoted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if st, _ := status(t, db, id); st != stage_orders.StagedOrderWatching {
		t.Fatalf("a dry run changed the order to %s", st)
	}
}

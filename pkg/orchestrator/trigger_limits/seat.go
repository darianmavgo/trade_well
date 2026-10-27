package trigger_limits

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// A strategy that limits 40+ names at once cannot rest them all: the broker refuses a
// buy limit once the working orders have used up the buying power. Seat puts as many of
// the day's limits at the broker before the open as it will take, nearest to filling
// first, and treats the broker's own refusal as the signal that the account is full (a
// cash account, a margin account and a different balance for each account all answer
// differently, so no count is worked out here). Once the market is open it keeps the
// resting set the nearest ones: a resting order that has become much farther from its
// limit than a name still watched is cancelled and the nearer name takes its place.
// The limits are fixed from the prior close, so an order is never repriced, only swapped.

const (
	seatFrom      = 9*60 + 0   // earliest minute orders are seated (ET)
	seatUntil     = 15*60 + 30 // no seating or swapping after this
	refillUntil   = 9*60 + 50  // until here every pass tries to fill free slots, not only after a swap
	maxSwaps      = 8
	cancelPolls   = 5
	cancelPollGap = 2 * time.Second
)

// SeatResult summarizes one account's Seat pass.
type SeatResult struct {
	Seated         int  // limits accepted by the broker
	Swapped        int  // resting orders replaced by a nearer name
	BuyingPowerHit bool // the broker said the account is full
	Rejected       int  // orders the broker refused (buying power included) or that failed to submit
	Stopped        bool // placing ended because Options.MaxFailures was reached
	Details        []string
	Rows           []Row
}

// Seat is one pass for acc: place what the broker will take, then (once the market is
// open) swap farther resting orders for nearer watched names. It does nothing outside
// the seating window, on a non-session day, or when no limit is being watched.
func Seat(ctx context.Context, db *sql.DB, tc *trader.TraderClient, acc *trader.ResolvedAccount, opts Options) (*SeatResult, error) {
	res := &SeatResult{}
	if db == nil || acc == nil {
		return res, fmt.Errorf("trigger_limits: seat needs a database and an account")
	}
	if tc == nil && !opts.DryRun {
		return res, nil
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	et := now.In(job_runs.EasternTime())
	today := et.Format("2006-01-02")
	minutes := et.Hour()*60 + et.Minute()
	todayUTC := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
	if !opts.AnySession && (!calendar.IsSession(todayUTC) || minutes < seatFrom || minutes >= seatUntil) {
		return res, nil
	}
	// Seat places buys straight at the broker, so it honours the risk halt itself. Orders
	// already resting stay; they are limits staged before the halt and fill only at their price.
	if rs, rerr := risk.Load(db, acc.AccountNumber); rerr == nil && rs.HaltEntries {
		res.Details = append(res.Details, fmt.Sprintf("[trigger_limits] %s: no new limits seated: %s", acc.AccountNumber, rs))
		return res, nil
	}

	if opts.Quotes == nil {
		return res, fmt.Errorf("trigger_limits: no quote source")
	}

	watching, err := todaysWatching(db, acc.AccountNumber, today, opts.AnySession)
	if err != nil || len(watching) == 0 {
		return res, err
	}
	resting, err := stage_orders.GetRestingDayEntries(db, acc.AccountNumber, today)
	if err != nil {
		return res, err
	}
	quotes, err := opts.Quotes(ctx, symbolsOf(watching, resting))
	if err != nil {
		return res, fmt.Errorf("trigger_limits: quotes: %w", err)
	}

	st := &seatState{
		db: db, tc: tc, acc: acc, opts: opts, now: now, res: res,
		held:          stage_orders.BrokerHeld(db, acc, acc.AccountNumber),
		filledByMonth: map[string]map[string]bool{},
	}

	marketOpen := minutes >= openMinutes
	swapped := false
	if marketOpen && len(resting) > 0 && !opts.NoCancel {
		swapped, err = st.swap(ctx, today, quotes, resting)
		if err != nil {
			return res, err
		}
		watching, err = todaysWatching(db, acc.AccountNumber, today, opts.AnySession)
		if err != nil {
			return res, err
		}
	}
	if (opts.NoCancel || minutes < refillUntil || swapped) && !res.BuyingPowerHit {
		if err := st.fill(ctx, RankByDistance(watching, quotes)); err != nil {
			return res, err
		}
	}
	return res, nil
}

func todaysWatching(db *sql.DB, accountNumber, today string, anySession bool) ([]stage_orders.StagedOrder, error) {
	all, err := stage_orders.GetWatchingStagedOrders(db, accountNumber)
	if err != nil {
		return nil, err
	}
	var out []stage_orders.StagedOrder
	for _, o := range all {
		if anySession || sessionOf(o) == today {
			out = append(out, o)
		}
	}
	return out, nil
}

func symbolsOf(sets ...[]stage_orders.StagedOrder) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for _, o := range set {
			s := strings.ToUpper(o.Symbol)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

type seatState struct {
	db            *sql.DB
	tc            *trader.TraderClient
	acc           *trader.ResolvedAccount
	opts          Options
	now           time.Time
	res           *SeatResult
	held          map[string]bool
	filledByMonth map[string]map[string]bool
	failures      int
}

func (s *seatState) row(o stage_orders.StagedOrder, status, detail string) {
	s.res.Rows = append(s.res.Rows, Row{Account: s.acc.AccountNumber, Strategy: o.StrategyName, Symbol: o.Symbol, Status: status, Detail: detail})
	s.res.Details = append(s.res.Details, fmt.Sprintf("[%s] %s: %s (staged id=%d)", o.Symbol, status, detail, o.ID))
}

// fill places the ranked watched limits one at a time, nearest first, until the broker
// refuses one for buying power. A refused row goes back to WATCHING.
func (s *seatState) fill(ctx context.Context, ranked []Ranked) error {
	equity, _ := stage_orders.AccountCapital(s.acc)
	for _, r := range ranked {
		if s.res.Stopped {
			return nil
		}
		o := r.Order
		sym := strings.ToUpper(o.Symbol)
		if reason := skipReason(s.db, s.opts.Members, o, s.held, s.filledByMonth, s.now); reason != "" {
			s.skip(o, reason)
			continue
		}
		qty := o.Quantity
		if qty < 1 {
			alloc := o.AllocPct
			if alloc <= 0 || alloc > 1 {
				alloc = 0.65
			}
			qty = math.Floor(equity * alloc / o.EntryPrice)
		}
		if qty < 1 {
			s.skip(o, "less than one share at the strategy's position size")
			continue
		}
		detail := fmt.Sprintf("%.0f shares @ $%.2f, %.2f%% below the market", qty, o.EntryPrice, 100*r.Distance)
		if s.opts.DryRun {
			s.row(o, "WOULD_SEAT", detail)
			continue
		}
		if _, err := stage_orders.PromoteWatchingOrder(s.db, o.ID, qty); err != nil {
			if s.opts.MaxFailures > 0 {
				s.failed(o, "FAILED", "promote: "+err.Error())
				continue
			}
			return fmt.Errorf("promote staged order %d: %w", o.ID, err)
		}
		if _, err := submit_orders.Run(ctx, s.db, s.tc, s.acc, false); err != nil {
			if s.opts.MaxFailures > 0 {
				s.failed(o, "FAILED", "submit: "+err.Error())
				continue
			}
			return fmt.Errorf("submit staged order %d: %w", o.ID, err)
		}
		got, err := stage_orders.GetStagedOrder(s.db, o.ID)
		if err != nil || got == nil {
			return fmt.Errorf("reload staged order %d: %v", o.ID, err)
		}
		switch got.Status {
		case stage_orders.StagedOrderRejected:
			if IsBuyingPowerRefusal(got.RejectReason) {
				if _, err := stage_orders.ReopenRejectedWatch(s.db, o.ID); err != nil {
					return err
				}
				s.res.BuyingPowerHit = true
				s.failed(o, "BUYING_POWER_FULL", "the broker refused it for buying power, so the account is full: "+got.RejectReason)
				if s.opts.MaxFailures == 0 {
					return nil
				}
				continue
			}
			s.failed(o, "REJECTED", got.RejectReason)
		case stage_orders.StagedOrderPending:
			s.failed(o, "PENDING", "not accepted by the broker; left pending for the next submit_orders run")
		default:
			s.res.Seated++
			s.held[sym] = true
			s.row(o, "SEATED", fmt.Sprintf("%s: %s (order %s)", detail, got.Status, got.OrderID))
		}
	}
	return nil
}

// skip reports a watched order that must not be placed. With Options.NoCancel it stays
// WATCHING; otherwise it is closed.
func (s *seatState) skip(o stage_orders.StagedOrder, reason string) {
	if s.opts.NoCancel {
		s.row(o, "SKIPPED", reason)
		return
	}
	s.row(o, "CANCELLED", reason)
	if !s.opts.DryRun {
		_ = stage_orders.CancelWatchingOrder(s.db, o.ID, reason)
	}
}

// failed reports an order the broker refused or that did not go through, and ends the
// placing once Options.MaxFailures of them have happened.
func (s *seatState) failed(o stage_orders.StagedOrder, status, detail string) {
	s.row(o, status, detail)
	s.res.Rejected++
	s.failures++
	if s.opts.MaxFailures > 0 && s.failures >= s.opts.MaxFailures {
		s.res.Stopped = true
		s.res.Details = append(s.res.Details, fmt.Sprintf("STOPPED after %d refused or failed orders; no more orders submitted", s.failures))
	}
}

// swap cancels the farthest resting order whenever a watched name is meaningfully nearer
// to filling, and seats the nearer name. It reports whether any resting order was cancelled.
func (s *seatState) swap(ctx context.Context, today string, quotes map[string]Quote, resting []stage_orders.StagedOrder) (bool, error) {
	any := false
	for i := 0; i < maxSwaps; i++ {
		watching, err := todaysWatching(s.db, s.acc.AccountNumber, today, s.opts.AnySession)
		if err != nil {
			return any, err
		}
		out, in, ok := PlanSwap(RankByDistance(resting, quotes), RankByDistance(watching, quotes))
		if !ok {
			return any, nil
		}
		detail := fmt.Sprintf("%s (%.2f%% from its limit) gives way to %s (%.2f%%)", out.Order.Symbol, 100*out.Distance, in.Order.Symbol, 100*in.Distance)
		if s.opts.DryRun {
			s.row(out.Order, "WOULD_SWAP", detail)
			return any, nil
		}
		cancelled, err := s.cancelResting(ctx, out.Order, "swapped for "+in.Order.Symbol+", nearer its limit")
		if err != nil {
			return any, err
		}
		if !cancelled {
			return any, nil // not confirmed cancelled: leave it to submit_orders to reconcile
		}
		any = true
		s.res.Swapped++
		s.row(out.Order, "SWAPPED_OUT", detail)
		// The cancelled name stays a candidate, behind the nearer one.
		back := out.Order
		back.ID, back.Status, back.OrderID, back.ClientOrderID, back.RejectReason, back.SubmittedAt = 0, stage_orders.StagedOrderWatching, "", "", "", nil
		back.StagedAt = s.now.UTC()
		if _, err := stage_orders.InsertStagedOrder(s.db, &back); err != nil {
			return any, fmt.Errorf("re-watch %s: %w", out.Order.Symbol, err)
		}
		delete(s.held, strings.ToUpper(out.Order.Symbol))
		watching, err = todaysWatching(s.db, s.acc.AccountNumber, today, s.opts.AnySession)
		if err != nil {
			return any, err
		}
		before := s.res.Seated
		if err := s.fill(ctx, RankByDistance(watching, quotes)); err != nil {
			return any, err
		}
		if s.res.BuyingPowerHit && s.res.Seated == before {
			return any, nil // freed buying power is not usable yet; the next pass refills
		}
		if resting, err = stage_orders.GetRestingDayEntries(s.db, s.acc.AccountNumber, today); err != nil {
			return any, err
		}
		s.res.BuyingPowerHit = false
	}
	return any, nil
}

// cancelResting cancels a working entry at the broker and waits for the broker to say it
// is CANCELED before closing the row, because buying power is only freed then. It reports
// false (touching nothing) when the order was not confirmed cancelled, e.g. it filled.
func (s *seatState) cancelResting(ctx context.Context, o stage_orders.StagedOrder, reason string) (bool, error) {
	if err := s.tc.Client.CancelOrder(ctx, s.acc.HashValue, o.OrderID); err != nil {
		s.row(o, "CANCEL_FAILED", err.Error())
		return false, nil
	}
	for i := 0; i < cancelPolls; i++ {
		ord, err := s.tc.Client.GetOrder(ctx, s.acc.HashValue, o.OrderID)
		if err == nil && strings.EqualFold(ord.Status, "CANCELED") {
			if _, err := stage_orders.CancelRestingOrder(s.db, o.ID, reason); err != nil {
				return false, err
			}
			_, _ = manage_exits.CancelPendingByOrder(s.db, s.acc.AccountNumber, o.OrderID, reason)
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(cancelPollGap):
		}
	}
	s.row(o, "CANCEL_UNCONFIRMED", "the broker did not report it CANCELED; left for submit_orders to reconcile")
	return false, nil
}

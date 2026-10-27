// Package submit_orders re-validates pkg/stage_orders' PENDING rows against
// the account's current state and places them with Schwab. On a confirmed
// fill it calls pkg/manage_exits.Open instead of writing manage_exit itself
// -- trade-lifecycle ownership stays in one package.
package submit_orders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/orderintent"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Row is one outcome for the operator email report. Deliberately not
// pkg/send_email's type -- submit_orders stays decoupled from the email
// package and lets the caller (cmd/orchestrator) translate.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Signal   string
	Status   string
	Detail   string
}

// Result summarizes a run of the order-submission job.
type Result struct {
	StagedOrdersFound int
	Submitted         int
	Rejected          int
	Details           []string
	Rows              []Row
}

// orderLookback is how far back checkOrderFill searches for an order. It must
// span a weekend: an order entered Friday is reconciled Monday morning.
const orderLookback = 7 * 24 * time.Hour

// maxOrderLookback is the furthest back Schwab lets an order query reach (its
// window is 60 days; stay inside it).
const maxOrderLookback = 58 * 24 * time.Hour

// lookbackStart is the start of the window checkOrderFill searches. Entries are
// GOOD_TILL_CANCEL limits that can rest for weeks, so a fixed 7 days would lose
// an order that fills on day 8+ ("not found") and its position would never be
// tracked for its time-based exit. Reach back to a day before the order was
// placed (never less than orderLookback, never past Schwab's window).
func lookbackStart(now, placedAt time.Time) time.Time {
	from := now.Add(-orderLookback)
	if !placedAt.IsZero() {
		if p := placedAt.Add(-24 * time.Hour); p.Before(from) {
			from = p
		}
	}
	if floor := now.Add(-maxOrderLookback); from.Before(floor) {
		from = floor
	}
	return from
}

// stagedPlacedAt is when a staged order reached the broker (its staging time when the
// submit time was never recorded).
func stagedPlacedAt(so stage_orders.StagedOrder) time.Time {
	if so.SubmittedAt != nil && !so.SubmittedAt.IsZero() {
		return *so.SubmittedAt
	}
	return so.StagedAt
}

// checkOrderFill asks the broker for orderID's current status among recent orders,
// upper-cased (e.g. "FILLED", "WORKING", "CANCELED"), plus the time the order reached its
// final state (the fill time, for a FILLED order); zero when the broker gave none. The status
// is "" if the order isn't found in that window -- broker-side propagation lag, or a lookup
// failure -- which callers must treat as "still unconfirmed," never as any particular state.
//
// placedAt (zero if unknown) widens the search window for long-resting orders;
// see lookbackStart.
func checkOrderFill(ctx context.Context, tc *trader.TraderClient, now, placedAt time.Time, orderID string) (string, time.Time) {
	d := checkOrderDetail(ctx, tc, now, placedAt, orderID)
	return d.Status, d.Closed
}

// orderDetail is what the broker reports about one order.
type orderDetail struct {
	Status    string    // upper-cased; "" when not found
	Closed    time.Time // when it reached its final state; zero when the broker gave none
	FilledQty float64   // shares executed so far
}

// checkOrderDetail is checkOrderFill plus the executed quantity, which is how a partial fill
// is told apart from an order that never filled.
func checkOrderDetail(ctx context.Context, tc *trader.TraderClient, now, placedAt time.Time, orderID string) orderDetail {
	if tc == nil || orderID == "" {
		return orderDetail{}
	}
	raw, err := tc.Client.ListOrders(ctx, schwab.OrderListRequest{
		From: lookbackStart(now, placedAt),
		To:   now,
	})
	if err != nil {
		return orderDetail{}
	}
	for _, ord := range schwab.FlattenOrders(raw) {
		if ord.OrderID != nil && fmt.Sprintf("%d", *ord.OrderID) == orderID {
			d := orderDetail{Status: strings.ToUpper(ord.Status), FilledQty: ord.FilledQuantity}
			if ord.CloseTime != nil {
				d.Closed = ord.CloseTime.Time
			}
			return d
		}
	}
	return orderDetail{}
}

// endedPartlyFilled reports whether an order the broker has closed without a full fill still
// executed some shares. Those shares are a real position with no exit tracked.
func endedPartlyFilled(status string, filledQty float64) bool {
	switch status {
	case "CANCELED", "REJECTED", "EXPIRED":
		return filledQty > 0
	}
	return false
}

// tradeParams is the manage_exit row for a staged order. The decision key is
// copied from the buy signal when that row exists; the limit stays EntryPrice.
func tradeParams(db *sql.DB, acc *trader.ResolvedAccount, so stage_orders.StagedOrder, orderID string, entered, filledAt time.Time) manage_exits.OpenTradeParams {
	d, _ := scan_market.DecisionForStagedOrder(db, so.ID, so.SignalID)
	strategyID := d.StrategyID
	if strategyID == "" {
		strategyID = so.StrategyName
	}
	return manage_exits.OpenTradeParams{
		AccountNumber:     acc.AccountNumber,
		Symbol:            so.Symbol,
		StrategyName:      so.StrategyName,
		EntryOrderID:      orderID,
		EntryPrice:        so.EntryPrice,
		TargetProfitPrice: so.TakeProfitPrice,
		StopLossPrice:     so.StopLossPrice,
		Quantity:          so.Quantity,
		EntryTime:         entered,
		FilledAt:          filledAt,
		MaxTradingDays:    so.MaxTradingDays,
		StrategyID:        strategyID,
		AsOfDate:          d.AsOfDate,
		NextSession:       d.NextSession,
		SignalID:          so.SignalID,
		BuySignalID:       d.BuySignalID,
		StageOrderID:      so.ID,
		FilledQty:         so.Quantity,
	}
}

// entryTime is when a position really opened: the broker's fill time when it
// gave one, else now. Time-based exits count trading days from this, so
// recording the confirmation time instead would hold a position a day too long
// whenever the fill is confirmed on a later run.
func entryTime(filledAt, now time.Time) time.Time {
	if filledAt.IsZero() || filledAt.After(now) {
		return now
	}
	return filledAt.UTC()
}

// openTrade registers a filled position with manage_exits, or promotes the entry that
// registerPending recorded when the order was accepted. filledAt is the broker's fill
// time (zero when it gave none): with it the exit date is final, computed from the fill
// date; without it the date stays provisional. A failure here is the worst silent
// outcome in the job -- shares are held with no exit tracking -- so it is surfaced as a
// red ERROR row and a detail line rather than swallowed.
func openTrade(db *sql.DB, acc *trader.ResolvedAccount, so stage_orders.StagedOrder, orderID string, filledAt, now time.Time, res *Result) {
	entered := entryTime(filledAt, now)
	if filledAt.IsZero() || filledAt.After(now) {
		filledAt = time.Time{} // not a usable fill time
	}
	_, err := manage_exits.Open(db, tradeParams(db, acc, so, orderID, entered, filledAt))
	if err == nil {
		return
	}
	msg := fmt.Sprintf("order %s filled but the manage_exit row was NOT created (%v) -- position has no exit tracking, fix by hand", orderID, err)
	res.Details = append(res.Details, fmt.Sprintf("[%s] %s (staged id=%d)", so.Symbol, msg, so.ID))
	res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: so.StrategyName, Symbol: so.Symbol, Signal: so.Instruction, Status: "ERROR", Detail: msg})
}

// registerPending records an accepted entry that is not yet confirmed filled, with a
// provisional exit date, so an exit date exists from the moment the order is placed.
// manage_exits corrects it from the real fill date (openTrade at confirmation, or
// manage_exits.Run from the broker's order list). A failure is an ERROR row.
func registerPending(db *sql.DB, acc *trader.ResolvedAccount, so stage_orders.StagedOrder, orderID string, now time.Time, res *Result) {
	_, err := manage_exits.RegisterPending(db, tradeParams(db, acc, so, orderID, now, time.Time{}))
	if err == nil {
		return
	}
	msg := fmt.Sprintf("order %s accepted but no provisional exit date could be recorded (%v)", orderID, err)
	res.Details = append(res.Details, fmt.Sprintf("[%s] %s (staged id=%d)", so.Symbol, msg, so.ID))
	res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: so.StrategyName, Symbol: so.Symbol, Signal: so.Instruction, Status: "ERROR", Detail: msg})
}

// cancelPending drops the provisional trade of an entry that died unfilled.
func cancelPending(db *sql.DB, acc *trader.ResolvedAccount, orderID, status string) {
	_, _ = manage_exits.CancelPendingByOrder(db, acc.AccountNumber, orderID, "entry "+strings.ToLower(status))
}

// Run does two things for acc, in order:
//
//  1. Re-checks every staged order accepted on a prior run but never
//     confirmed filled (StagedOrderPendingFill) -- PlaceOrder succeeding only
//     means accepted. A limit order can sit WORKING indefinitely, or come
//     back CANCELED/REJECTED/EXPIRED without ever filling; manage_exit must
//     never gain a row for a position that was never actually opened.
//  2. Re-validates every PENDING staged order against acc's current state
//     (cash, positions) before submitting it live -- rather than trusting
//     whatever was true when the scan staged it -- and then applies the same
//     fill check before treating it as an open position.
//
// dryRun previews what would be submitted/rejected without calling the broker.
func Run(
	ctx context.Context,
	db *sql.DB,
	tc *trader.TraderClient,
	acc *trader.ResolvedAccount,
	dryRun bool,
) (*Result, error) {
	res := &Result{}
	now := time.Now().UTC()

	if db == nil {
		return res, fmt.Errorf("no database connection")
	}
	if err := EnsureSchema(db); err != nil {
		return res, fmt.Errorf("submit_orders: ensure schema: %w", err)
	}

	// seen is a fast-path hint only; durable uniqueness is client_order_id +
	// stage_orders.ClaimStagedOrderForSubmit (PENDING -> SUBMITTING) at the DB layer.
	seen := make(map[string]bool)

	pendingFill, err := stage_orders.GetOrdersAwaitingFillConfirmation(db, acc.AccountNumber)
	if err != nil {
		res.Details = append(res.Details, fmt.Sprintf("Failed to load fill-pending orders: %v", err))
	}
	for _, so := range pendingFill {
		seen[strings.ToUpper(strings.TrimSpace(so.Symbol))+"|"+so.Instruction] = true
		reconcilePendingFill(ctx, db, acc, tc, now, dryRun, so, res)
	}

	submitting, err := stage_orders.GetSubmittingStagedOrders(db, acc.AccountNumber)
	if err != nil {
		res.Details = append(res.Details, fmt.Sprintf("Failed to load SUBMITTING orders: %v", err))
	}
	for _, so := range submitting {
		seen[strings.ToUpper(strings.TrimSpace(so.Symbol))+"|"+so.Instruction] = true
		reconcileSubmitting(ctx, db, acc, tc, now, dryRun, so, res)
	}

	staged, err := stage_orders.GetPendingStagedOrders(db, acc.AccountNumber)
	if err != nil {
		return res, fmt.Errorf("failed to load staged orders: %w", err)
	}
	res.StagedOrdersFound = len(staged) + len(pendingFill) + len(submitting)

	// Defense in depth against duplicate PENDING rows for the same
	// (symbol, instruction). Oldest-staged first; later duplicates rejected.
	for _, so := range staged {
		row := Row{Account: acc.AccountNumber, Strategy: so.StrategyName, Symbol: so.Symbol, Signal: so.Instruction}
		baseDetail := fmt.Sprintf("%.0f shares @ $%.2f -> TP $%.2f, SL $%.2f (staged id=%d)",
			so.Quantity, so.EntryPrice, so.TakeProfitPrice, so.StopLossPrice, so.ID)

		key := strings.ToUpper(strings.TrimSpace(so.Symbol)) + "|" + so.Instruction
		if seen[key] {
			row.Status = "DUPLICATE_REJECTED"
			row.Detail = baseDetail + " -- an earlier PENDING/fill-pending/SUBMITTING row for this symbol was already handled in this run"
			res.Details = append(res.Details, fmt.Sprintf("[%s] Rejecting duplicate PENDING %s order (staged id=%d): an earlier PENDING/fill-pending/SUBMITTING row for this symbol was already handled in this run", so.Symbol, so.Instruction, so.ID))
			res.Rejected++
			res.Rows = append(res.Rows, row)
			if !dryRun {
				if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, "duplicate PENDING order for this symbol in the same account"); merr != nil {
					res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
				}
				_ = scan_market.UpdateSignalStatus(db, so.SignalID, "DUPLICATE_REJECTED")
			}
			continue
		}
		seen[key] = true

		entry := trader.RoundPrice(so.EntryPrice)
		tp := trader.RoundPrice(so.TakeProfitPrice)
		stop := trader.RoundPrice(so.StopLossPrice)
		qty := so.Quantity
		entryType := trader.OrderTypeLimit
		if entry != so.EntryPrice || tp != so.TakeProfitPrice || stop != so.StopLossPrice {
			res.Details = append(res.Details, fmt.Sprintf(
				"[%s] Rounded prices to cents: entry $%.2f TP $%.2f SL $%.2f (Schwab 2-decimal rule)",
				so.Symbol, entry, tp, stop))
			if !dryRun {
				_, _ = db.Exec(orchestratorsql.Q("submit_orders/run"),
					entry, tp, stop, so.ID)
				so.EntryPrice, so.TakeProfitPrice, so.StopLossPrice = entry, tp, stop
			}
		}

		if stop <= 0 && entry > 0 {
			stop = trader.RoundPrice(entry * trader.CrisisStopFraction) // -20% crisis guard; strategy SL 0 = no strategy stop
			res.Details = append(res.Details, fmt.Sprintf("[%s] Strategy SL is 0 — applying crisis guard stop $%.2f (-20%%)", so.Symbol, stop))
			if !dryRun {
				_, _ = db.Exec(orchestratorsql.Q("submit_orders/run_2"), stop, so.ID)
				so.StopLossPrice = stop
			}
		}
		entry = trader.RoundPrice(entry)
		tp = trader.RoundPrice(tp)
		stop = trader.RoundPrice(stop)
		order, err := trader.BuildBracketOrderSpecDuration(so.Symbol, qty, entry, tp, stop, entryType, entryDurationFor(so))
		if err != nil {
			row.Status = "ERROR_BUILD_ORDER"
			row.Detail = baseDetail + fmt.Sprintf(" -- failed to build bracket order: %v", err)
			res.Details = append(res.Details, fmt.Sprintf("[%s] Failed to build bracket order: %v", so.Symbol, err))
			if !dryRun {
				if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, fmt.Sprintf("failed to build order: %v", err)); merr != nil {
					res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
				}
				_ = scan_market.UpdateSignalStatus(db, so.SignalID, "ERROR_BUILD_ORDER")
			}
			res.Rejected++
			res.Rows = append(res.Rows, row)
			continue
		}

		// Last gate before the broker: the risk state (see pkg/risk) can have tightened since
		// this order was staged. Entries only; exits never come through here.
		if so.Instruction == trader.InstructionBuy {
			reason, status := riskGate(db, acc, so, dryRun, now)
			if reason != "" {
				row.Status = status
				row.Detail = baseDetail + " -- " + reason
				res.Details = append(res.Details, fmt.Sprintf("[%s] Not submitted (staged id=%d): %s", so.Symbol, so.ID, reason))
				res.Rejected++
				res.Rows = append(res.Rows, row)
				// An unreadable risk state leaves the order PENDING: the next run retries it.
				// A real limit marks it rejected, as before.
				if !dryRun && status != "RISK_UNREADABLE" {
					if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, "risk: "+reason); merr != nil {
						res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
					}
					_ = scan_market.UpdateSignalStatus(db, so.SignalID, status)
				}
				continue
			}
		}

		if tc == nil {
			row.Status = "PENDING"
			row.Detail = baseDetail + " -- no live trader client available; left pending (order built OK; safety check and broker preview not run)"
			res.Details = append(res.Details, fmt.Sprintf("[%s] No live trader client available; leaving staged order id=%d pending", so.Symbol, so.ID))
			res.Rows = append(res.Rows, row)
			continue
		}

		// Pre-trade Safety Validation against the account's current state.
		if _, err := tc.Safety.ValidateOrder(order, &acc.Details, entry); err != nil {
			row.Status = "SAFETY_REJECTED"
			row.Detail = baseDetail + fmt.Sprintf(" -- %v", err)
			if dryRun {
				row.Detail += " (dry-run preview)"
			}
			res.Details = append(res.Details, fmt.Sprintf("[%s] Safety check rejected order (staged id=%d): %v", so.Symbol, so.ID, err))
			res.Rejected++
			res.Rows = append(res.Rows, row)
			if dryRun {
				continue
			}
			if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, err.Error()); merr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
			}
			_ = scan_market.UpdateSignalStatus(db, so.SignalID, "SAFETY_REJECTED")
			continue
		}

		if dryRun {
			// Ask Schwab to validate the exact order it would receive (non-mutating).
			if perr := previewRejects(ctx, tc, acc, order); perr != nil {
				row.Status = "PREVIEW_REJECTED"
				row.Detail = baseDetail + " -- broker preview: " + perr.Error() + " (dry-run)"
				res.Details = append(res.Details, fmt.Sprintf("[%s] Broker preview rejected order (staged id=%d): %v", so.Symbol, so.ID, perr))
				res.Rejected++
				res.Rows = append(res.Rows, row)
				continue
			}
			row.Status = "SIMULATED"
			row.Detail = baseDetail + " -- would submit (built, safety-checked and broker-previewed)"
			res.Details = append(res.Details, fmt.Sprintf("[%s] Would submit bracket order (staged id=%d): %.0f shares @ $%.2f", so.Symbol, so.ID, so.Quantity, so.EntryPrice))
			res.Rows = append(res.Rows, row)
			continue
		}

		tradeDate := now.Format("2006-01-02")
		clientOrderID := so.ClientOrderID
		if clientOrderID == "" {
			clientOrderID = orderintent.FromStagedKey(acc.AccountNumber, so.Symbol, so.Instruction, tradeDate, so.ID)
		}
		claimed, cerr := stage_orders.ClaimStagedOrderForSubmit(db, so.ID, clientOrderID)
		if cerr != nil {
			row.Status = "FAILED_CLAIM"
			row.Detail = baseDetail + fmt.Sprintf(" -- claim failed: %v", cerr)
			res.Details = append(res.Details, fmt.Sprintf("[%s] Claim failed (staged id=%d): %v", so.Symbol, so.ID, cerr))
			res.Rejected++
			res.Rows = append(res.Rows, row)
			continue
		}
		if !claimed {
			row.Status = "CLAIM_LOST"
			row.Detail = baseDetail + " -- another worker claimed this row"
			res.Details = append(res.Details, fmt.Sprintf("[%s] Claim lost (staged id=%d) — skipping", so.Symbol, so.ID))
			res.Rows = append(res.Rows, row)
			continue
		}
		so.ClientOrderID = clientOrderID
		so.Status = stage_orders.StagedOrderSubmitting
		order.ClientOrderID = clientOrderID

		orderIDFull, err := tc.Client.PlaceOrder(ctx, acc.HashValue, order)
		if err != nil && schwab.IsReauthRequired(err) {
			// The refresh token is dead, so nothing was sent: this is not a rejection of the
			// order. Put it back to PENDING for the next run and stop; every other order
			// would fail the same way.
			if rerr := stage_orders.ReleaseStagedOrderClaim(db, so.ID); rerr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] could not release staged id=%d back to PENDING: %v (it is SUBMITTING; reconcile will resolve it)", so.Symbol, so.ID, rerr))
			}
			row.Status = "AUTH_FAILED"
			row.Detail = baseDetail + " -- Schwab refused our credentials; the order was not sent and stays PENDING"
			res.Rows = append(res.Rows, row)
			logAttempt(db, so.ID, "", so.Symbol, so.Quantity, "AUTH_FAILED", err.Error(), now)
			return res, fmt.Errorf("Schwab refresh token is invalid, expired or revoked, so no order could be placed (staged id=%d and the rest stay PENDING): run `orchestrator auth`, then submit_orders again: %w", so.ID, err)
		}
		if err != nil {
			short, full := describeSubmitFailure(err)
			row.Status = "FAILED_SUBMIT"
			row.Detail = baseDetail + " -- submission failed: " + short
			res.Details = append(res.Details, fmt.Sprintf("[%s] Order placement failed (staged id=%d, client_order_id=%s): %s", so.Symbol, so.ID, clientOrderID, full))
			res.Rejected++
			res.Rows = append(res.Rows, row)
			logAttempt(db, so.ID, "", so.Symbol, so.Quantity, "FAILED_SUBMIT", full, now)
			// Ambiguous errors leave SUBMITTING for reconcile; clear rejects mark REJECTED.
			if isAmbiguousSubmitErr(err) {
				res.Details = append(res.Details, fmt.Sprintf("[%s] Ambiguous submit error — leaving SUBMITTING for reconcile (client_order_id=%s)", so.Symbol, clientOrderID))
			} else {
				if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, fmt.Sprintf("submission failed: %v", err)); merr != nil {
					res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
				}
				_ = scan_market.UpdateSignalStatus(db, so.SignalID, "FAILED_SUBMIT")
			}
			continue
		}
		orderID := schwab.LocationOrderID(orderIDFull)
		// that it filled -- check its real status before ever treating this
		// as an open position.
		fillStatus, filledAt := checkOrderFill(ctx, tc, now, time.Time{}, orderID) // just placed
		switch fillStatus {
		case "FILLED":
			res.Submitted++
			res.Details = append(res.Details, fmt.Sprintf("[%s] ✓ Bracket order submitted and confirmed FILLED! Order ID: %s (staged id=%d)", so.Symbol, orderID, so.ID))
			row.Status = "SUBMITTED"
			row.Detail = baseDetail + fmt.Sprintf(" -- order ID %s, confirmed filled", orderID)
			res.Rows = append(res.Rows, row)
			if merr := stage_orders.MarkStagedOrderSubmitted(db, so.ID, orderID, now); merr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark submitted: %v", so.Symbol, merr))
			}
			_ = scan_market.UpdateSignalStatus(db, so.SignalID, "EXECUTED")
			openTrade(db, acc, so, orderID, filledAt, now, res)
			logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, "FILLED", "confirmed filled", now)
		case "CANCELED", "REJECTED", "EXPIRED":
			reason := fmt.Sprintf("order accepted (ID %s) but broker reports it %s before ever filling -- no position was opened", orderID, strings.ToLower(fillStatus))
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s (staged id=%d).", so.Symbol, reason, so.ID))
			row.Status = "CANCELED_UNFILLED"
			row.Detail = baseDetail + " -- " + reason
			res.Rejected++
			res.Rows = append(res.Rows, row)
			if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, reason); merr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected: %v", so.Symbol, merr))
			}
			_ = scan_market.UpdateSignalStatus(db, so.SignalID, "CANCELED_UNFILLED")
			cancelPending(db, acc, orderID, fillStatus)
			logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, fillStatus, reason, now)
		default:
			res.Submitted++
			res.Details = append(res.Details, fmt.Sprintf("[%s] Order accepted (ID %s, staged id=%d) but not yet confirmed filled -- will re-check next run.", so.Symbol, orderID, so.ID))
			row.Status = "SUBMITTED_PENDING_FILL"
			row.Detail = baseDetail + fmt.Sprintf(" -- order ID %s accepted, fill not yet confirmed", orderID)
			res.Rows = append(res.Rows, row)
			if merr := stage_orders.MarkStagedOrderPendingFill(db, so.ID, orderID, now); merr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark pending-fill: %v", so.Symbol, merr))
			}
			registerPending(db, acc, so, orderID, now, res)
			logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, "SUBMITTED_PENDING_FILL", "accepted, fill not yet confirmed", now)
		}
	}

	return res, nil
}

// reconcilePendingFill re-checks a StagedOrderPendingFill order's real
// status and resolves it: FILLED promotes it to a confirmed SUBMITTED row
// plus a new manage_exit position; CANCELED/REJECTED/EXPIRED marks it
// REJECTED with no position ever created; anything else (still WORKING, or
// not found in the lookback window) is left as-is for the next run.
func reconcilePendingFill(
	ctx context.Context,
	db *sql.DB,
	acc *trader.ResolvedAccount,
	tc *trader.TraderClient,
	now time.Time,
	dryRun bool,
	so stage_orders.StagedOrder,
	res *Result,
) {
	row := Row{Account: acc.AccountNumber, Strategy: so.StrategyName, Symbol: so.Symbol, Signal: so.Instruction}
	baseDetail := fmt.Sprintf("%.0f shares @ $%.2f -> TP $%.2f, SL $%.2f (staged id=%d, order %s)",
		so.Quantity, so.EntryPrice, so.TakeProfitPrice, so.StopLossPrice, so.ID, so.OrderID)

	if tc == nil {
		row.Status = "SUBMITTED_PENDING_FILL"
		row.Detail = baseDetail + " -- no live trader client available; fill still unconfirmed"
		res.Rows = append(res.Rows, row)
		return
	}

	detail := checkOrderDetail(ctx, tc, now, stagedPlacedAt(so), so.OrderID)
	status, filledAt := detail.Status, detail.Closed
	if endedPartlyFilled(status, detail.FilledQty) {
		// Not "never filled": some shares are held and nothing tracks their exit. Say so on
		// every run until a person adopts or sells them; the staged row is left as it is.
		msg := fmt.Sprintf("broker reports %s after filling %.0f of %.0f shares: those shares are held with no exit tracked -- adopt them (manage_exits) or sell them",
			strings.ToLower(status), detail.FilledQty, so.Quantity)
		res.Details = append(res.Details, fmt.Sprintf("[%s] PARTIAL FILL (staged id=%d, order %s): %s.", so.Symbol, so.ID, so.OrderID, msg))
		row.Status = "PARTIAL_FILL_ACTION_NEEDED"
		row.Detail = baseDetail + " -- " + msg
		res.Rows = append(res.Rows, row)
		return
	}
	switch status {
	case "FILLED":
		res.Details = append(res.Details, fmt.Sprintf("[%s] Confirmed filled (staged id=%d, order %s).", so.Symbol, so.ID, so.OrderID))
		row.Status = "SUBMITTED"
		row.Detail = baseDetail + " -- confirmed filled"
		res.Rows = append(res.Rows, row)
		res.Submitted++
		if dryRun {
			return
		}
		if merr := stage_orders.MarkStagedOrderSubmitted(db, so.ID, so.OrderID, now); merr != nil {
			res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark submitted after fill confirm: %v", so.Symbol, merr))
		}
		if err := scan_market.UpdateSignalStatus(db, so.SignalID, "EXECUTED"); err != nil {
			res.Details = append(res.Details, fmt.Sprintf("[%s] failed to update signal EXECUTED: %v", so.Symbol, err))
		}
		openTrade(db, acc, so, so.OrderID, filledAt, now, res)
		logAttempt(db, so.ID, so.OrderID, so.Symbol, so.Quantity, "FILLED", "confirmed filled (reconciled)", now)
	case "CANCELED", "REJECTED", "EXPIRED":
		reason := fmt.Sprintf("broker reports %s before ever filling -- no position was opened", strings.ToLower(status))
		res.Details = append(res.Details, fmt.Sprintf("[%s] %s (staged id=%d, order %s).", so.Symbol, reason, so.ID, so.OrderID))
		row.Status = "CANCELED_UNFILLED"
		row.Detail = baseDetail + " -- " + reason
		res.Rejected++
		res.Rows = append(res.Rows, row)
		if !dryRun {
			if merr := stage_orders.MarkStagedOrderRejected(db, so.ID, reason); merr != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to mark rejected after broker %s: %v", so.Symbol, status, merr))
			}
			if err := scan_market.UpdateSignalStatus(db, so.SignalID, "CANCELED_UNFILLED"); err != nil {
				res.Details = append(res.Details, fmt.Sprintf("[%s] failed to update signal CANCELED_UNFILLED: %v", so.Symbol, err))
			}
			cancelPending(db, acc, so.OrderID, status)
			logAttempt(db, so.ID, so.OrderID, so.Symbol, so.Quantity, status, reason, now)
		}
	default:
		unconfirmed := status
		if unconfirmed == "" {
			unconfirmed = "not found in lookback window"
		}
		row.Status = "SUBMITTED_PENDING_FILL"
		row.Detail = baseDetail + fmt.Sprintf(" -- still not confirmed filled (broker status: %s)", unconfirmed)
		res.Rows = append(res.Rows, row)
	}
}

// reconcileSubmitting handles staged rows left in SUBMITTING after a crash.
// Never blind re-PlaceOrder: prefer intent store ack, else broker list match,
// else ConfirmPlacedOrder if order_id already known.
func reconcileSubmitting(
	ctx context.Context,
	db *sql.DB,
	acc *trader.ResolvedAccount,
	tc *trader.TraderClient,
	now time.Time,
	dryRun bool,
	so stage_orders.StagedOrder,
	res *Result,
) {
	row := Row{Account: acc.AccountNumber, Strategy: so.StrategyName, Symbol: so.Symbol, Signal: so.Instruction}
	baseDetail := fmt.Sprintf("%.0f shares @ $%.2f (staged id=%d, client_order_id=%s)",
		so.Quantity, so.EntryPrice, so.ID, so.ClientOrderID)

	if dryRun {
		row.Status = "SUBMITTING"
		row.Detail = baseDetail + " -- would reconcile"
		res.Rows = append(res.Rows, row)
		return
	}
	if tc == nil || tc.Client == nil {
		row.Status = "SUBMITTING"
		row.Detail = baseDetail + " -- no trader client; left for later reconcile"
		res.Rows = append(res.Rows, row)
		return
	}

	// If schwaber intent store already acked, promote.
	if store := tc.Client.IntentStore(); store != nil && so.ClientOrderID != "" {
		if in, err := store.GetIntent(ctx, so.ClientOrderID); err == nil && in != nil && in.Status == "acked" && in.BrokerOrderID != "" {
			orderID := in.BrokerOrderID
			fillStatus, filledAt := checkOrderFill(ctx, tc, now, stagedPlacedAt(so), orderID)
			applyPostPlaceStatus(ctx, db, acc, now, so, orderID, fillStatus, filledAt, baseDetail, row, res)
			return
		}
		if _, err := schwab.ReconcilePendingIntents(ctx, tc.Client); err != nil {
			res.Details = append(res.Details, fmt.Sprintf("[%s] intent reconcile: %v", so.Symbol, err))
		}
		if in, err := store.GetIntent(ctx, so.ClientOrderID); err == nil && in != nil && in.Status == "acked" && in.BrokerOrderID != "" {
			orderID := in.BrokerOrderID
			fillStatus, filledAt := checkOrderFill(ctx, tc, now, stagedPlacedAt(so), orderID)
			applyPostPlaceStatus(ctx, db, acc, now, so, orderID, fillStatus, filledAt, baseDetail, row, res)
			return
		}
		if in, err := store.GetIntent(ctx, so.ClientOrderID); err == nil && in != nil && in.Status == "failed" {
			_ = stage_orders.MarkStagedOrderRejected(db, so.ID, "intent failed: "+in.Error)
			row.Status = "FAILED_SUBMIT"
			row.Detail = baseDetail + " -- intent failed: " + in.Error
			res.Rejected++
			res.Rows = append(res.Rows, row)
			return
		}
	}

	row.Status = "SUBMITTING"
	row.Detail = baseDetail + " -- still unresolved; not re-placing"
	res.Details = append(res.Details, fmt.Sprintf("[%s] SUBMITTING staged id=%d still unresolved (client_order_id=%s) — not re-placing", so.Symbol, so.ID, so.ClientOrderID))
	res.Rows = append(res.Rows, row)
}

func applyPostPlaceStatus(
	ctx context.Context,
	db *sql.DB,
	acc *trader.ResolvedAccount,
	now time.Time,
	so stage_orders.StagedOrder,
	orderID, fillStatus string,
	filledAt time.Time,
	baseDetail string,
	row Row,
	res *Result,
) {
	switch fillStatus {
	case "FILLED":
		res.Submitted++
		row.Status = "SUBMITTED"
		row.Detail = baseDetail + fmt.Sprintf(" -- order ID %s, confirmed filled", orderID)
		res.Rows = append(res.Rows, row)
		_ = stage_orders.MarkStagedOrderSubmitted(db, so.ID, orderID, now)
		_ = scan_market.UpdateSignalStatus(db, so.SignalID, "EXECUTED")
		openTrade(db, acc, so, orderID, filledAt, now, res)
		logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, "FILLED", "confirmed filled (reconciled submitting)", now)
	case "CANCELED", "REJECTED", "EXPIRED":
		reason := fmt.Sprintf("order %s broker reports %s", orderID, strings.ToLower(fillStatus))
		row.Status = "CANCELED_UNFILLED"
		row.Detail = baseDetail + " -- " + reason
		res.Rejected++
		res.Rows = append(res.Rows, row)
		_ = stage_orders.MarkStagedOrderRejected(db, so.ID, reason)
		cancelPending(db, acc, orderID, fillStatus)
		logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, fillStatus, reason, now)
	default:
		res.Submitted++
		row.Status = "SUBMITTED_PENDING_FILL"
		row.Detail = baseDetail + fmt.Sprintf(" -- order ID %s accepted, fill not yet confirmed", orderID)
		res.Rows = append(res.Rows, row)
		_ = stage_orders.MarkStagedOrderPendingFill(db, so.ID, orderID, now)
		registerPending(db, acc, so, orderID, now, res)
		logAttempt(db, so.ID, orderID, so.Symbol, so.Quantity, "SUBMITTED_PENDING_FILL", "accepted, fill not yet confirmed", now)
	}
}

// describeSubmitFailure explains a failed PlaceOrder. short is for the email
// row and reject_reason; full is for the log and the submit_order audit row and,
// for a Schwab refusal, carries the endpoint, HTTP status, Schwab's response,
// its correlation ID (what Schwab support asks for) and the exact JSON sent.
func describeSubmitFailure(err error) (short, full string) {
	var apiErr *schwab.APIError
	if !errors.As(err, &apiErr) {
		return err.Error(), err.Error()
	}
	short = fmt.Sprintf("Schwab refused the order (HTTP %d): %s", apiErr.Status, apiErr.Body)
	if apiErr.CorrelID != "" {
		short += " [schwab_correl_id=" + apiErr.CorrelID + "]"
	}
	full = apiErr.Detail()
	if apiErr.Status == 400 && strings.Contains(strings.ToLower(apiErr.Body), "validation error") {
		full += " -- Schwab gave no field-level reason; compare the request above with an accepted order for what differs"
	}
	return short, full
}

func isAmbiguousSubmitErr(err error) bool {
	if err == nil {
		return false
	}
	// A structured Schwab answer decides by status: 4xx is a definite refusal,
	// only 5xx is unknown. Matching text would read a body containing e.g.
	// "unknown" as ambiguous and leave a refused order SUBMITTING forever.
	var apiErr *schwab.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status >= 500
	}
	msg := strings.ToLower(err.Error())
	for _, n := range []string{"ambiguous", "unknown", "timeout", "connection reset", "eof", "status 5"} {
		if strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

// entryDurationFor is the entry order duration for a staged order: its own
// (stage_orders.EntryDurationDay makes a limit that expires at the close), otherwise the
// default the order builder picks.
func entryDurationFor(so stage_orders.StagedOrder) string {
	if so.EntryDuration == stage_orders.EntryDurationDay {
		return trader.DurationDay
	}
	return ""
}

// riskGate is the last risk check before a buy goes to the broker. It returns "" when the order
// may go, otherwise the reason and the row status (RISK_HALTED, RISK_LIMIT or RISK_UNREADABLE).
//
// It fails closed: a live run that cannot read the risk state places nothing, because the
// halt and the order caps live in that state. A dry run only reports the problem and goes on,
// so a preview still works on a fresh database.
func riskGate(db *sql.DB, acc *trader.ResolvedAccount, so stage_orders.StagedOrder, dryRun bool, now time.Time) (reason, status string) {
	rs, err := risk.Load(db, acc.AccountNumber)
	if err != nil {
		if dryRun {
			return "", ""
		}
		return fmt.Sprintf("risk state unreadable, buy not placed: %v", err), "RISK_UNREADABLE"
	}
	if rs.HaltEntries {
		return rs.String(), "RISK_HALTED"
	}
	equity, _ := stage_orders.AccountCapital(acc)
	et := now.In(job_runs.EasternTime())
	sessionStart := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, job_runs.EasternTime())
	today, open, err := risk.Counts(db, acc.AccountNumber, sessionStart)
	if err != nil {
		if dryRun {
			return "", ""
		}
		return err.Error(), "RISK_UNREADABLE"
	}
	if why := rs.CheckEntry(so.Quantity*so.EntryPrice, equity, today, open); why != "" {
		return why, "RISK_LIMIT"
	}
	return "", ""
}

// previewRejects sends order to Schwab's previewOrder (nothing is placed) and returns an
// error when the call fails or the answer carries validation rejects.
func previewRejects(ctx context.Context, tc *trader.TraderClient, acc *trader.ResolvedAccount, order *schwab.Order) error {
	raw, err := tc.Client.PreviewOrder(ctx, acc.HashValue, order)
	if err != nil {
		return err
	}
	var ans struct {
		Result struct {
			Rejects []struct {
				Message string `json:"message"`
			} `json:"rejects"`
		} `json:"orderValidationResult"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return fmt.Errorf("unreadable preview answer: %w", err)
	}
	var msgs []string
	for _, r := range ans.Result.Rejects {
		msgs = append(msgs, r.Message)
	}
	if len(msgs) > 0 {
		return fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	return nil
}

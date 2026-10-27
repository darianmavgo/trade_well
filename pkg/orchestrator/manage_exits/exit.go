package manage_exits

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// WorkingOrders returns every cancellable order in orders (children of brackets
// included) as a flat list, in list order.
func WorkingOrders(orders []schwab.Order) []schwab.Order {
	var out []schwab.Order
	for _, o := range schwab.FlattenOrders(orders) {
		if schwab.IsWorkingStatus(o.Status) && o.OrderID != nil {
			out = append(out, o)
		}
	}
	return out
}

// CancelOrders cancels each order by id and returns the ids it cancelled with the
// errors of those it could not. An order the broker says is already CANCELED (a bracket
// child that went with its parent) counts as cancelled, and a 429 is retried with
// backoff; calls are paced so a bracket-heavy account does not trip the rate limit.
func CancelOrders(ctx context.Context, tc *trader.TraderClient, hash string, orders []schwab.Order) (cancelled []string, errs []error) {
	seen := map[int64]bool{}
	for _, o := range orders {
		if o.OrderID == nil || seen[*o.OrderID] {
			continue
		}
		seen[*o.OrderID] = true
		id := fmt.Sprintf("%d", *o.OrderID)
		err := cancelWithRetry(ctx, tc, hash, id)
		if err != nil && strings.Contains(err.Error(), "state CANCELED cannot be canceled") {
			err = nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("cancel order %s: %w", id, err))
			continue
		}
		cancelled = append(cancelled, id)
		// Cancelling a parent takes its legs with it; do not spend calls on them.
		for _, k := range schwab.FlattenOrders(o.Children()) {
			if k.OrderID != nil {
				seen[*k.OrderID] = true
			}
		}
		select {
		case <-ctx.Done():
			return cancelled, append(errs, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	return cancelled, errs
}

func cancelWithRetry(ctx context.Context, tc *trader.TraderClient, hash, id string) error {
	backoff := time.Second
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = tc.Client.CancelOrder(ctx, hash, id)
		if err == nil || !strings.Contains(err.Error(), "429") {
			return err
		}
		fmt.Fprintf(os.Stderr, "  rate limited cancelling order %s; retrying in %s\n", id, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return err
}

// blocksSale reports whether o is a working sell of sym, or contains one.
// A bracket's OCO parent is WORKING and has no legs of its own; the sells are its children.
func blocksSale(o schwab.Order, sym string) bool {
	if schwab.IsWorkingStatus(o.Status) && trader.OrderSymbol(&o) == sym && trader.OrderIsSell(&o) {
		return true
	}
	for _, k := range o.Children() {
		if blocksSale(k, sym) {
			return true
		}
	}
	return false
}

// ordersBlockingSale are the working orders to cancel before a market sell of symbol.
// That is each resting sell, and the working parent that wraps it. Schwab will not
// release the shares when only the OCO's child legs are cancelled; the parent is
// the order that holds them. A filled entry (the TRIGGER) is not working, so it is
// left alone. Parents come out before their children.
func ordersBlockingSale(orders []schwab.Order, symbol string) []schwab.Order {
	sym := strings.ToUpper(symbol)
	var out []schwab.Order
	seen := map[int64]bool{}
	var walk func([]schwab.Order)
	walk = func(list []schwab.Order) {
		for i := range list {
			o := list[i]
			if o.OrderID != nil && schwab.IsWorkingStatus(o.Status) && !seen[*o.OrderID] && blocksSale(o, sym) {
				seen[*o.OrderID] = true
				out = append(out, o)
			}
			walk(o.Children())
		}
	}
	walk(orders)
	return out
}

// ExitAtMarket sells qty shares of symbol at market for acc. It first cancels every
// working order that reserves the shares (the sell legs and the OCO parent that
// wraps them), then checks with the broker that none is left, places the market
// sell, and confirms the broker accepted it. recent is the caller's recent order
// list. It is the one place a position is sold at market: manage_exits and
// exit_market both use it.
func ExitAtMarket(ctx context.Context, tc *trader.TraderClient, acc *trader.ResolvedAccount, symbol string, qty float64, recent []schwab.Order) error {
	if tc == nil {
		return fmt.Errorf("no broker client")
	}
	if qty <= 0 {
		return fmt.Errorf("nothing to sell: quantity %.0f", qty)
	}
	sym := strings.ToUpper(symbol)
	if blocking := ordersBlockingSale(recent, sym); len(blocking) > 0 {
		_, cancelErrs := CancelOrders(ctx, tc, acc.HashValue, blocking)
		// Trust the broker, not the cancel replies: what is still working now?
		// The same 30-day window Run uses, so a hold longer than a week is still visible.
		now := time.Now()
		raw, err := tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{From: now.AddDate(0, 0, -30), To: now})
		if err != nil {
			return fmt.Errorf("could not confirm the %s exit orders were cancelled: %w", sym, err)
		}
		if left := ordersBlockingSale(raw, sym); len(left) > 0 {
			return fmt.Errorf("%d working order(s) on %s could not be cancelled, so the shares are still reserved (%v)", len(left), sym, cancelErrs)
		}
	}
	spec, err := trader.BuildOrderSpec(trader.OrderTypeMarket, trader.InstructionSell, sym, qty, nil, trader.DurationDay, trader.SessionNormal)
	if err != nil {
		return fmt.Errorf("build sell: %w", err)
	}
	loc, err := tc.Client.PlaceOrder(ctx, acc.HashValue, spec)
	if err != nil {
		return fmt.Errorf("place sell: %w", err)
	}
	// PlaceOrder 201 != filled. Confirm broker status before success.
	id, status, desc, cerr := schwab.ConfirmPlacedOrder(ctx, tc.Client, acc.HashValue, loc)
	if cerr != nil {
		return fmt.Errorf("place sell returned %s but status confirm failed: %w", loc, cerr)
	}
	if schwab.IsTerminalReject(status) {
		if desc == "" {
			desc = status
		}
		return fmt.Errorf("market sell REJECTED id=%s status=%s (%s)", id, status, desc)
	}
	return nil
}

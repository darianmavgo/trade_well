// Package orders is the IBKR twin of schwaber/pkg/orders: place (buy/sell), bracket, list
// and cancel. Each takes a built TraderClient, writes to out, and returns an error; the
// caller (cmd/ibkr) decides the exit code. Preview is the default: nothing is sent unless
// Live is true and DryRun is false, and a live order asks for confirmation unless Yes.
package orders

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	"github.com/darianmavgo/ibkr_api/pkg/trader"
)

// PlaceOptions are the inputs to Place.
type PlaceOptions struct {
	Instruction string  // trader.InstructionBuy or InstructionSell
	Symbol      string  // ticker
	Qty         float64 // shares
	Price       float64 // limit price; 0 = market order
	Account     string  // "" = the first configured account
	DryRun      bool
	Live        bool
	Yes         bool
}

// BracketOptions are the inputs to Bracket.
type BracketOptions struct {
	Symbol  string
	Qty     float64
	Entry   float64 // entry limit price
	Profit  float64 // take-profit; <=0 = entry +10%
	Stop    float64 // stop-loss; <=0 = entry -5%
	Account string
	DryRun  bool
	Live    bool
	Yes     bool
}

func isLive(dry, live bool) bool { return live && !dry }

func confirm(out io.Writer, in io.Reader, prompt string, yes bool) bool {
	if yes {
		return true
	}
	fmt.Fprintf(out, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

func account(ctx context.Context, tc *trader.TraderClient, want string) (*trader.ResolvedAccount, error) {
	id := want
	if id == "" && len(tc.Config.AccountIDs) > 0 {
		id = tc.Config.AccountIDs[0]
	}
	if id == "" {
		return nil, fmt.Errorf("no account: pass -account or set IBKR_ACCOUNT_ID")
	}
	accs, err := tc.GetAccounts(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	return accs[0], nil
}

// submit previews (what-if), or places and confirms, one built order.
func submit(ctx context.Context, out io.Writer, in io.Reader, tc *trader.TraderClient, acc *trader.ResolvedAccount, order *ibkr.Order, live, yes bool, summary string) error {
	fmt.Fprintf(out, "%s\n", summary)
	if !live {
		raw, err := tc.Client.PreviewOrder(ctx, acc.HashValue, order)
		if err != nil {
			return fmt.Errorf("preview failed: %w", err)
		}
		fmt.Fprintf(out, "DRY RUN: nothing was placed. IBKR's what-if answer:\n%s\n", raw)
		return nil
	}
	if !confirm(out, in, "Place this order LIVE?", yes) {
		fmt.Fprintln(out, "cancelled: nothing placed.")
		return nil
	}
	id, err := tc.Client.PlaceOrder(ctx, acc.HashValue, order)
	if err != nil {
		var ae *ibkr.APIError
		if asAPI(err, &ae) {
			return fmt.Errorf("%w\n%s", err, ae.Detail())
		}
		return err
	}
	oid, status, desc, err := tc.Client.ConfirmPlacedOrder(ctx, acc.HashValue, id)
	if err != nil {
		return fmt.Errorf("order %s was accepted but its status could not be read: %w", id, err)
	}
	fmt.Fprintf(out, "order %s: %s %s\n", oid, status, desc)
	if ibkr.IsTerminalReject(status) {
		return fmt.Errorf("order %s is %s", oid, status)
	}
	return nil
}

// Place places (or previews) one order.
func Place(ctx context.Context, out io.Writer, in io.Reader, tc *trader.TraderClient, o PlaceOptions) error {
	sym := strings.ToUpper(strings.TrimSpace(o.Symbol))
	acc, err := account(ctx, tc, o.Account)
	if err != nil {
		return err
	}
	var price *float64
	otype := trader.OrderTypeMarket
	if o.Price > 0 {
		p := trader.RoundPrice(o.Price)
		price, otype = &p, trader.OrderTypeLimit
	}
	order, err := trader.BuildOrderSpec(otype, o.Instruction, sym, o.Qty, price, trader.DurationDay, trader.SessionNormal)
	if err != nil {
		return err
	}
	var market float64
	if q, qerr := tc.Client.GetQuote(ctx, sym); qerr == nil {
		market = trader.ExtractQuoteLastPrice(q)
	}
	v, err := tc.Safety.ValidateOrder(order, &acc.Details, market)
	if err != nil {
		return fmt.Errorf("safety check refused the order: %w", err)
	}
	for _, w := range v.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	summary := fmt.Sprintf("%s %.0f %s %s on %s (~$%.2f)", o.Instruction, o.Qty, sym, otype, acc.AccountNumber, v.EstimatedValue)
	return submit(ctx, out, in, tc, acc, order, isLive(o.DryRun, o.Live), o.Yes, summary)
}

// Bracket places (or previews) entry + take-profit + stop-loss as one IBKR bracket.
func Bracket(ctx context.Context, out io.Writer, in io.Reader, tc *trader.TraderClient, o BracketOptions) error {
	sym := strings.ToUpper(strings.TrimSpace(o.Symbol))
	acc, err := account(ctx, tc, o.Account)
	if err != nil {
		return err
	}
	if o.Profit <= 0 {
		o.Profit = o.Entry * 1.10
	}
	if o.Stop <= 0 {
		o.Stop = o.Entry * 0.95
	}
	order, err := trader.BuildBracketOrderSpec(sym, o.Qty, o.Entry, o.Profit, o.Stop, trader.OrderTypeLimit)
	if err != nil {
		return err
	}
	if _, err := tc.Safety.ValidateOrder(order, &acc.Details, o.Entry); err != nil {
		return fmt.Errorf("safety check refused the order: %w", err)
	}
	summary := fmt.Sprintf("BRACKET %s %.0f: buy limit $%.2f, take-profit $%.2f, stop $%.2f on %s", sym, o.Qty, o.Entry, o.Profit, o.Stop, acc.AccountNumber)
	return submit(ctx, out, in, tc, acc, order, isLive(o.DryRun, o.Live), o.Yes, summary)
}

// List prints the session's orders for the configured accounts (status filters, "" = all).
func List(ctx context.Context, out io.Writer, tc *trader.TraderClient, status string) error {
	orders, err := tc.Client.ListOrders(ctx, ibkr.OrderListRequest{Status: strings.ToUpper(status)})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%-12s %-10s %-6s %-5s %-6s %-11s %-10s %s\n", "Account", "Order", "Symbol", "Side", "Qty", "Type", "Price", "Status")
	for _, o := range orders {
		legs, _ := trader.ParseOrderLegs(&o)
		sym, side := "", ""
		if len(legs) > 0 {
			sym, side = legs[0].Instrument.Symbol, legs[0].Instruction
		}
		id, price := "", "-"
		if o.OrderID != nil {
			id = fmt.Sprint(*o.OrderID)
		}
		if o.Price != nil {
			price = fmt.Sprintf("%.2f", *o.Price)
		} else if o.StopPrice != nil {
			price = fmt.Sprintf("stop %.2f", *o.StopPrice)
		}
		fmt.Fprintf(out, "%-12s %-10s %-6s %-5s %-6.0f %-11s %-10s %s\n", o.AccountNumber, id, sym, side, o.Quantity, o.OrderType, price, o.Status)
	}
	if len(orders) == 0 {
		fmt.Fprintln(out, "(no orders this session)")
	}
	return nil
}

// Cancel cancels one order by id on the configured account.
func Cancel(ctx context.Context, out io.Writer, tc *trader.TraderClient, account, orderID string) error {
	if account == "" && len(tc.Config.AccountIDs) > 0 {
		account = tc.Config.AccountIDs[0]
	}
	if account == "" {
		return fmt.Errorf("no account: pass -account or set IBKR_ACCOUNT_ID")
	}
	if err := tc.Client.CancelOrder(ctx, account, orderID); err != nil {
		return err
	}
	fmt.Fprintf(out, "cancel requested for order %s; status: ", orderID)
	time.Sleep(500 * time.Millisecond)
	if o, err := tc.Client.GetOrder(ctx, account, orderID); err == nil {
		fmt.Fprintln(out, o.Status)
	} else {
		fmt.Fprintln(out, "unknown")
	}
	return nil
}

func asAPI(err error, target **ibkr.APIError) bool {
	for err != nil {
		if ae, ok := err.(*ibkr.APIError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

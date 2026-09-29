// Package killswitch is the IBKR twin of schwaber's killswitch: cancel working orders and
// market-sell open long positions, all or by ticker. Preview unless Live.
package killswitch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	"github.com/darianmavgo/ibkr_api/pkg/trader"
)

// Options are the inputs to Run.
type Options struct {
	DryRun  bool
	Live    bool
	Yes     bool
	Account string   // "" = every configured account
	Tickers []string // empty = every position
}

// Run cancels every cancelable order of the matching accounts, then market-sells the
// matching long positions. Nothing is sent unless Live and not DryRun.
func Run(ctx context.Context, out io.Writer, in io.Reader, tc *trader.TraderClient, o Options) error {
	want := map[string]bool{}
	for _, t := range o.Tickers {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
			want[t] = true
		}
	}
	live := o.Live && !o.DryRun
	ids := tc.Config.AccountIDs
	if o.Account != "" {
		ids = []string{o.Account}
	}
	if len(ids) == 0 {
		return fmt.Errorf("no account: pass -account or set IBKR_ACCOUNT_ID")
	}
	accs, err := tc.GetAccounts(ctx, ids)
	if err != nil {
		return err
	}
	type sell struct {
		acc *trader.ResolvedAccount
		sym string
		qty float64
	}
	var sells []sell
	var cancels []struct{ acc, id string }
	for _, a := range accs {
		orders, err := tc.Client.ListAccountOrders(ctx, a.HashValue, ibkr.OrderListRequest{})
		if err != nil {
			return err
		}
		for _, ord := range orders {
			if ord.Cancelable != nil && *ord.Cancelable && ord.OrderID != nil {
				legs, _ := trader.ParseOrderLegs(&ord)
				if len(want) == 0 || (len(legs) > 0 && want[strings.ToUpper(legs[0].Instrument.Symbol)]) {
					cancels = append(cancels, struct{ acc, id string }{a.HashValue, fmt.Sprint(*ord.OrderID)})
				}
			}
		}
		for _, p := range a.Details.Positions {
			sym := strings.ToUpper(p.Instrument.Symbol)
			if p.LongQuantity > 0 && (len(want) == 0 || want[sym]) {
				sells = append(sells, sell{a, sym, p.LongQuantity})
			}
		}
	}
	fmt.Fprintf(out, "KILLSWITCH: %d order(s) to cancel, %d position(s) to market-sell", len(cancels), len(sells))
	if !live {
		fmt.Fprintln(out, "  [DRY RUN: nothing sent; pass -live to act]")
		for _, s := range sells {
			fmt.Fprintf(out, "  would SELL %.0f %s on %s at market\n", s.qty, s.sym, s.acc.AccountNumber)
		}
		return nil
	}
	fmt.Fprintln(out)
	if !o.Yes {
		fmt.Fprint(out, "This is LIVE. Type YES to continue: ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(line) != "YES" {
			fmt.Fprintln(out, "aborted: nothing sent.")
			return nil
		}
	}
	failed := 0
	for _, c := range cancels {
		if err := tc.Client.CancelOrder(ctx, c.acc, c.id); err != nil {
			failed++
			fmt.Fprintf(out, "  cancel %s failed: %v\n", c.id, err)
		}
	}
	for _, s := range sells {
		spec, err := trader.BuildOrderSpec(trader.OrderTypeMarket, trader.InstructionSell, s.sym, s.qty, nil, trader.DurationDay, trader.SessionNormal)
		if err != nil {
			failed++
			fmt.Fprintf(out, "  %s: %v\n", s.sym, err)
			continue
		}
		id, err := tc.Client.PlaceOrder(ctx, s.acc.HashValue, spec)
		if err != nil {
			failed++
			fmt.Fprintf(out, "  SELL %s failed: %v\n", s.sym, err)
			continue
		}
		_, status, _, _ := tc.Client.ConfirmPlacedOrder(ctx, s.acc.HashValue, id)
		fmt.Fprintf(out, "  SELL %.0f %s on %s: order %s %s\n", s.qty, s.sym, s.acc.AccountNumber, id, status)
	}
	if failed > 0 {
		return fmt.Errorf("%d step(s) failed", failed)
	}
	return nil
}

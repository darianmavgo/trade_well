// Package killswitch is the `schwaber killswitch` command: market-sell open
// long positions across linked accounts. Default is every position; optional
// tickers limit the blast radius. Not scheduled: invoke it explicitly when you
// need the floor exit.
package killswitch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Options are the inputs to Run.
type Options struct {
	DryRun  bool     // preview market sells without placing
	Live    bool     // submit live market sells (DryRun always wins)
	Yes     bool     // skip the confirmation prompt
	Account string   // limit to one account number; "" = all linked
	Tickers []string // limit to these symbols; empty = every position
}

// Run market-sells the matching long positions. It cancels cancelable open
// orders first, prints progress to out, prompts on in unless Options.Yes, and
// returns clicolor.ErrReported when anything failed (already printed).
func Run(ctx context.Context, out io.Writer, in io.Reader, cfg *config.Config, o Options) error {
	tickers := map[string]bool{}
	for _, a := range o.Tickers {
		a = strings.ToUpper(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		tickers[a] = true
	}
	filter := len(tickers) > 0

	// Live unless --dry-run (or --live=false), and --dry-run always wins.
	isDry := o.DryRun || !o.Live
	isLive := o.Live && !o.DryRun
	autoYes := o.Yes

	clicolor.Banner(out)
	fmt.Fprintf(out, "%s[KILLSWITCH]%s Market-exit open positions", clicolor.Red, clicolor.Reset)
	if filter {
		var list []string
		for t := range tickers {
			list = append(list, t)
		}
		fmt.Fprintf(out, " (tickers: %s)", strings.Join(list, ", "))
	} else {
		fmt.Fprintf(out, " (ALL symbols)")
	}
	if o.Account != "" {
		fmt.Fprintf(out, " account=%s", o.Account)
	}
	fmt.Fprintln(out)
	if isDry {
		fmt.Fprintf(out, "%sMode: DRY-RUN (no orders placed)%s\n\n", clicolor.Yellow, clicolor.Reset)
	} else {
		fmt.Fprintf(out, "%sMode: LIVE MARKET SELLS%s\n\n", clicolor.Red, clicolor.Reset)
	}

	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sClient Error:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}
	linked, err := tc.Client.ListLinkedAccounts(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sFailed to list accounts:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	type target struct {
		account string
		hash    string
		symbol  string
		qty     float64
	}
	var targets []target
	wantAcc := strings.TrimSpace(o.Account)

	for _, la := range linked {
		if wantAcc != "" && la.AccountNumber != wantAcc {
			continue
		}
		acc, err := tc.Client.GetAccount(ctx, la.HashValue, "positions")
		if err != nil {
			fmt.Fprintf(out, "[%s] get account: %v\n", la.AccountNumber, err)
			continue
		}
		sec, err := trader.ParseSecuritiesAccount(acc.SecuritiesAccount)
		if err != nil {
			fmt.Fprintf(out, "[%s] parse: %v\n", la.AccountNumber, err)
			continue
		}
		for _, pos := range sec.Positions {
			sym := strings.ToUpper(pos.Instrument.Symbol)
			qty := pos.LongQuantity
			if qty <= 0 {
				continue
			}
			if filter && !tickers[sym] {
				continue
			}
			targets = append(targets, target{
				account: sec.AccountNumber,
				hash:    la.HashValue,
				symbol:  sym,
				qty:     qty,
			})
		}
	}

	if len(targets) == 0 {
		fmt.Fprintf(out, "%sNo matching long positions to exit.%s\n", clicolor.Yellow, clicolor.Reset)
		return nil
	}

	fmt.Fprintf(out, "%-12s %-8s %10s\n", "Account", "Symbol", "Qty")
	fmt.Fprintln(out, strings.Repeat("-", 36))
	for _, t := range targets {
		fmt.Fprintf(out, "%-12s %-8s %10.0f\n", t.account, t.symbol, t.qty)
	}
	fmt.Fprintln(out)

	if isLive && !autoYes {
		fmt.Fprintf(out, "%s⚠ LIVE KILLSWITCH — market-sell %d position(s)? (y/N): %s", clicolor.Red, len(targets), clicolor.Reset)
		line, _ := bufio.NewReader(in).ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			fmt.Fprintf(out, "%sAborted.%s\n", clicolor.Yellow, clicolor.Reset)
			return nil
		}
	}

	var placed, failed int

	// Cancel any cancelable open orders first. WORKING OCO children from filled
	// brackets make a flat market sell REJECT ("oversold / not enough BP").
	terminal := map[string]bool{
		"FILLED": true, "REJECTED": true, "CANCELED": true, "CANCELLED": true,
		"EXPIRED": true, "REPLACED": true, "IGNORED": true,
	}
	hashesDone := map[string]bool{}
	for _, tg := range targets {
		if hashesDone[tg.hash] {
			continue
		}
		hashesDone[tg.hash] = true
		if isDry {
			fmt.Fprintf(out, "[%s] would cancel open orders before sell\n", tg.account)
			continue
		}
		// Empty status = all recent; filter client-side so OCO parents aren't missed.
		ords, err := tc.Client.ListAccountOrders(ctx, tg.hash, schwab.OrderListRequest{
			From: time.Now().Add(-7 * 24 * time.Hour),
			To:   time.Now().Add(time.Hour),
		})
		if err != nil {
			fmt.Fprintf(out, "[%s] list open orders: %v\n", tg.account, err)
			continue
		}
		var canceled int
		for _, o := range ords {
			if o.OrderID == nil {
				continue
			}
			st := strings.ToUpper(o.Status)
			if terminal[st] {
				continue
			}
			if o.Cancelable != nil && !*o.Cancelable {
				continue
			}
			oid := fmt.Sprintf("%d", *o.OrderID)
			if err := tc.Client.CancelOrder(ctx, tg.hash, oid); err != nil {
				fmt.Fprintf(out, "[%s] cancel %s (%s): %v\n", tg.account, oid, o.Status, err)
				continue
			}
			fmt.Fprintf(out, "[%s] canceled open order %s (%s %s)\n", tg.account, oid, o.Status, o.OrderStrategyType)
			canceled++
		}
		if canceled > 0 {
			fmt.Fprintf(out, "[%s] waiting for %d cancel(s) to clear...\n", tg.account, canceled)
			time.Sleep(1500 * time.Millisecond)
		} else {
			fmt.Fprintf(out, "[%s] no cancelable open orders\n", tg.account)
		}
	}

	for _, t := range targets {
		fmt.Fprintf(out, "[%s] SELL %.0f %s MARKET", t.account, t.qty, t.symbol)
		order, err := trader.BuildOrderSpec(
			trader.OrderTypeMarket,
			trader.InstructionSell,
			t.symbol,
			t.qty,
			nil,
			trader.DurationDay,
			trader.SessionNormal,
		)
		if err != nil {
			fmt.Fprintf(out, " — build fail: %v\n", err)
			failed++
			continue
		}
		if isDry {
			fmt.Fprintf(out, " — DRY-RUN\n")
			placed++
			continue
		}
		loc, err := tc.Client.PlaceOrder(ctx, t.hash, order)
		if err != nil {
			fmt.Fprintf(out, " — FAIL place: %v\n", err)
			failed++
			continue
		}
		// PlaceOrder 201 != filled. Confirm status; REJECTED must count as fail.
		oid, st, desc, cerr := schwab.ConfirmPlacedOrder(ctx, tc.Client, t.hash, loc)
		if cerr != nil {
			fmt.Fprintf(out, " — FAIL status check: %v (not assuming success)\n", cerr)
			failed++
			continue
		}
		if schwab.IsTerminalReject(st) {
			if desc == "" {
				desc = st
			}
			fmt.Fprintf(out, " — %s id=%s (%s)\n", st, oid, desc)
			failed++
			continue
		}
		fmt.Fprintf(out, " — %s id=%s\n", st, oid)
		placed++
		time.Sleep(300 * time.Millisecond)
	}

	fmt.Fprintf(out, "\n%sKillswitch done:%s ok=%d failed=%d\n", clicolor.Green, clicolor.Reset, placed, failed)
	if failed > 0 {
		return clicolor.ErrReported
	}
	return nil
}

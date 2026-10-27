package trigger_limits

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// ProbeSymbols are liquid ETFs the probe rests limits on, one order each.
var ProbeSymbols = []string{
	"SPY", "QQQ", "IWM", "DIA", "XLK", "XLF", "XLE", "XLV", "XLI", "XLP", "XLY", "XLU", "XLB", "XLRE", "XLC",
	"VTI", "VOO", "VEA", "VWO", "EFA", "EEM", "AGG", "TLT", "IEF", "LQD", "HYG", "GLD", "SLV", "USO", "VNQ",
	"SMH", "SOXX", "XBI", "IBB", "KRE", "XHB", "XRT", "ITB", "IYR", "VGT", "VHT", "VFH", "VDE", "VIS", "VCR", "VDC", "VPU", "VAW",
}

// probePace is the gap between probe requests.
const probePace = 1500 * time.Millisecond

// ProbeResult is what one account's probe found.
type ProbeResult struct {
	Account   string
	Accepted  int
	Refusal   string // Schwab's refusal that ended the probe ("" when the symbols ran out)
	Cancelled int
	Left      []string // order ids not confirmed CANCELED: cancel these by hand
}

// Probe measures how many buy limits Schwab accepts for acc before it refuses one for
// buying power. It rests day-limit bracket entries sized like the strategy's (allocPct of
// equity each) far below the market so none can fill, stops at the first refusal, then
// cancels everything it placed and waits for each to show CANCELED.
func Probe(ctx context.Context, out io.Writer, tc *trader.TraderClient, acc *trader.ResolvedAccount, symbols []string, allocPct, belowPct float64) (*ProbeResult, error) {
	res := &ProbeResult{Account: acc.AccountNumber}
	equity, _ := stage_orders.AccountCapital(acc)
	if equity <= 0 {
		return res, fmt.Errorf("account %s: no equity", acc.AccountNumber)
	}
	raw, err := tc.Client.GetQuotes(ctx, symbols)
	if err != nil {
		return res, fmt.Errorf("quotes: %w", err)
	}
	var placed []string
	defer func() {
		for _, id := range placed {
			ok := false
			for i := 0; i < 8 && !ok; i++ {
				time.Sleep(probePace)
				cerr := tc.Client.CancelOrder(ctx, acc.HashValue, id)
				fmt.Fprintf(out, "  cancel %s try %d: err=%v\n", id, i+1, cerr)
				time.Sleep(probePace)
				o, gerr := tc.Client.GetOrder(ctx, acc.HashValue, id)
				if gerr == nil {
					fmt.Fprintf(out, "  order %s status=%s\n", id, o.Status)
					ok = strings.EqualFold(o.Status, "CANCELED")
				} else {
					fmt.Fprintf(out, "  order %s status lookup: %v\n", id, gerr)
				}
			}
			if ok {
				res.Cancelled++
			} else {
				res.Left = append(res.Left, id)
			}
		}
	}()

	for _, sym := range symbols {
		time.Sleep(probePace) // stay well inside Schwab's request rate limit
		q, ok := raw[sym]
		if !ok {
			continue
		}
		d, err := q.Detail()
		if err != nil || d.LastPrice <= 0 {
			continue
		}
		limit := trader.RoundPrice(d.LastPrice * (1 - belowPct))
		qty := math.Floor(equity * allocPct / limit)
		if qty < 1 {
			continue
		}
		order, err := trader.BuildBracketOrderSpecDuration(sym, qty, limit, trader.RoundPrice(limit*1.03), trader.RoundPrice(limit*0.95), trader.OrderTypeLimit, trader.DurationDay)
		if err != nil {
			return res, err
		}
		loc, err := tc.Client.PlaceOrder(ctx, acc.HashValue, order)
		if err != nil {
			var api *schwab.APIError
			body := err.Error()
			if errors.As(err, &api) {
				body = fmt.Sprintf("HTTP %d: %s", api.Status, api.Body)
			}
			fmt.Fprintf(out, "  %-5s %5.0f @ $%.2f  REFUSED  %s\n", sym, qty, limit, body)
			// Any refusal ends the probe: keep sending and a rate limit or a bad request
			// only gets worse. A buying-power refusal is the answer being measured.
			res.Refusal = body
			if !IsBuyingPowerRefusal(body) {
				res.Refusal = "NOT buying power, probe stopped: " + body
			}
			return res, nil
		}
		id := loc
		if i := strings.LastIndex(strings.TrimRight(loc, "/"), "/"); i >= 0 {
			id = strings.TrimRight(loc, "/")[i+1:]
		}
		placed = append(placed, id)
		res.Accepted++
		fmt.Fprintf(out, "  %-5s %5.0f @ $%.2f  accepted (order %s) location=%s\n", sym, qty, limit, id, loc)
	}
	return res, nil
}

// CancelOrders cancels each "account:orderid" pair in spec (comma separated), one at a time,
// re-sending the cancel until the order shows a final state, and writes one line per order with
// the status it ended in. A pair whose account is not in accts is skipped. It places nothing.
func CancelOrders(ctx context.Context, out io.Writer, tc *trader.TraderClient, accts []*trader.ResolvedAccount, spec string) {
	hash := map[string]string{}
	for _, a := range accts {
		hash[a.AccountNumber] = a.HashValue
	}
	for _, pair := range strings.Split(spec, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) != 2 || hash[parts[0]] == "" {
			fmt.Fprintf(out, "skip %q: unknown account\n", pair)
			continue
		}
		h, id := hash[parts[0]], parts[1]
		status := ""
		for try := 0; try < 8; try++ {
			time.Sleep(1500 * time.Millisecond)
			o, err := tc.Client.GetOrder(ctx, h, id)
			if err != nil {
				continue
			}
			status = strings.ToUpper(o.Status)
			if status == "CANCELED" || status == "FILLED" || status == "REJECTED" || status == "EXPIRED" {
				break
			}
			_ = tc.Client.CancelOrder(ctx, h, id)
		}
		fmt.Fprintf(out, "%s %s -> %s\n", parts[0], id, status)
	}
}

// ListOpenOrders writes every order of the last day that is not in a final state, per account.
// It changes nothing.
func ListOpenOrders(ctx context.Context, out io.Writer, tc *trader.TraderClient, accts []*trader.ResolvedAccount) {
	for _, a := range accts {
		time.Sleep(2 * time.Second)
		orders, err := tc.Client.ListAccountOrders(ctx, a.HashValue, schwab.OrderListRequest{From: time.Now().Add(-24 * time.Hour), To: time.Now().Add(time.Hour)})
		if err != nil {
			fmt.Fprintf(out, "%s: list failed: %v\n", a.AccountNumber, err)
			continue
		}
		for _, o := range schwab.FlattenOrders(orders) {
			st := strings.ToUpper(o.Status)
			if st == "CANCELED" || st == "FILLED" || st == "EXPIRED" || st == "REJECTED" || o.OrderID == nil {
				continue
			}
			fmt.Fprintf(out, "%s order %d status=%s type=%s entered=%v legs=%s\n", a.AccountNumber, *o.OrderID, st, o.OrderType, o.EnteredTime, string(o.OrderLegCollection))
		}
	}
}

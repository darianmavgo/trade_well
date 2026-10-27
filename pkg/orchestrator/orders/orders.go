// Package orders answers "what does Schwab say happened to my orders?": every order in the accounts,
// brackets unfolded, with what it really executed at. It is read-only: it lists, never places or
// cancels, and writes nothing. The caller reads the order list (strategy_status.FetchLive) and
// passes it in, so the same Rows serve the `orchestrator orders` command and the /orders page.
//
// A price column here is one of three different things and each is labelled: Limit and Stop are
// what was sent (a limit price, a trigger), Fill is what executed. They differ: a buy limit of
// 271.76 filled at 256.69, and a stop triggered at 217.41 fills below it on a gap.
package orders

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobName is the CLI subcommand, the /orders route and the log prefix.
const JobName = "orders"

// DefaultDays is how far back orders are listed unless asked otherwise.
const DefaultDays = 7

// Options narrow the list. Zero values mean no narrowing.
type Options struct {
	Symbol string    // upper-cased, exact
	Status string    // upper-cased broker status, exact (FILLED, WORKING, CANCELED, ...)
	Since  time.Time // orders entered at or after this time
}

// Row is one order.
type Row struct {
	Account  string  `json:"account"`
	OrderID  string  `json:"order_id"`
	ParentID string  `json:"parent_order_id,omitempty"` // the bracket entry this order hangs under
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"` // BUY or SELL
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	Quantity float64 `json:"quantity"`
	// Limit and Stop are what was sent. Fill is the average price actually executed and FillQty
	// the quantity that executed; nil when nothing did, which a working or canceled order never has.
	Limit   *float64  `json:"limit_price,omitempty"`
	Stop    *float64  `json:"stop_price,omitempty"`
	Fill    *float64  `json:"fill_price,omitempty"`
	FillQty float64   `json:"fill_quantity,omitempty"`
	Entered time.Time `json:"entered_at,omitzero"`
	Closed  time.Time `json:"closed_at,omitzero"` // for a filled order, when it filled
}

// Rows flattens each account's orders (bracket children included, tagged with their parent),
// applies opt, and returns them newest first. Accounts and their orders come from
// strategy_status.Live.Orders.
func Rows(byAccount map[string][]schwab.Order, opt Options) []Row {
	opt.Symbol, opt.Status = strings.ToUpper(strings.TrimSpace(opt.Symbol)), strings.ToUpper(strings.TrimSpace(opt.Status))
	var out []Row
	for acct, orders := range byAccount {
		for _, top := range orders {
			collect(&out, acct, top, "", opt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := latest(out[i]), latest(out[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		if out[i].Account != out[j].Account {
			return out[i].Account < out[j].Account
		}
		return out[i].OrderID > out[j].OrderID
	})
	return out
}

func latest(r Row) time.Time {
	if r.Closed.After(r.Entered) {
		return r.Closed
	}
	return r.Entered
}

func collect(out *[]Row, acct string, o schwab.Order, parent string, opt Options) {
	id := ""
	if o.OrderID != nil {
		id = fmt.Sprintf("%d", *o.OrderID)
	}
	sym := trader.OrderSymbol(&o)
	// An OCO wrapper has no instrument of its own; its children are the orders.
	if sym != "" && keep(o, sym, opt) {
		r := Row{Account: acct, OrderID: id, ParentID: parent, Symbol: sym, Type: o.OrderType, Status: strings.ToUpper(o.Status),
			Quantity: o.Quantity, Limit: o.Price, Stop: o.StopPrice}
		r.Side = "BUY"
		if trader.OrderIsSell(&o) {
			r.Side = "SELL"
		}
		if px, q, ok := o.FillPrice(""); ok {
			r.Fill, r.FillQty = &px, q
		}
		if o.EnteredTime != nil {
			r.Entered = o.EnteredTime.Time
		}
		if o.CloseTime != nil {
			r.Closed = o.CloseTime.Time
		}
		*out = append(*out, r)
	}
	next := parent
	if id != "" {
		next = id
	}
	for _, c := range o.Children() {
		collect(out, acct, c, next, opt)
	}
}

func keep(o schwab.Order, sym string, opt Options) bool {
	if opt.Symbol != "" && sym != opt.Symbol {
		return false
	}
	if opt.Status != "" && strings.ToUpper(o.Status) != opt.Status {
		return false
	}
	if !opt.Since.IsZero() && o.EnteredTime != nil && o.EnteredTime.Time.Before(opt.Since) {
		return false
	}
	return true
}

// Format writes rows as a text table. Account numbers are shown by their last four digits.
func Format(w io.Writer, rows []Row) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ACCOUNT\tORDER\tSYMBOL\tSIDE\tTYPE\tSTATUS\tQTY\tLIMIT\tSTOP\tFILL\tCLOSED (ET)")
	for _, r := range rows {
		fmt.Fprintf(tw, "...%s\t%s\t%s\t%s\t%s\t%s\t%g\t%s\t%s\t%s\t%s\n", last4(r.Account), r.OrderID, r.Symbol, r.Side, r.Type, r.Status,
			r.Quantity, price(r.Limit), price(r.Stop), fill(r), eastern(r.Closed))
	}
	tw.Flush()
}

func last4(s string) string {
	if len(s) > 4 {
		return s[len(s)-4:]
	}
	return s
}

func price(p *float64) string {
	if p == nil || *p == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", *p)
}

func fill(r Row) string {
	if r.Fill == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f x %g", *r.Fill, r.FillQty)
}

func eastern(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 01-02 15:04:05")
}

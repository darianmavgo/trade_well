package stage_orders

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
)

// A strategy whose config sets EntryLimitPct sends a day limit order for every name it
// lists, each sized at its own share of equity, and most of them never fill (the limit
// is a dip the name usually does not make). A working buy order reserves its cash at
// the broker, so placing them all at the open would fund the first few names
// alphabetically and refuse the rest, which is not the strategy the backtest ran: there
// cash is spent only on the orders that fill. These orders are therefore staged as
// WATCHING, with no cash claimed, and pkg/trigger_limits places each one when the day's
// low reaches its limit. WatchEntries is the staging half.

// WatchEntries turns the signals of limit-entry strategies into WATCHING candidates:
// held, pending and already-watched names are skipped, nothing is claimed from cash,
// and Quantity is only the share count the order would have at full size (it is
// re-sized against the cash left when the limit triggers).
func WatchEntries(equity float64, held map[string]bool, cands []scan_market.Signal) []SizedEntry {
	out := make([]SizedEntry, 0, len(cands))
	seen := map[string]bool{}
	for k, v := range held {
		if v {
			seen[strings.ToUpper(k)] = true
		}
	}
	for _, c := range cands {
		e := SizedEntry{Signal: c, Watch: true}
		sym := strings.ToUpper(strings.TrimSpace(c.Symbol))
		switch {
		case sym == "":
			e.Reason = "empty symbol"
		case seen[sym]:
			e.Reason = "already holding or pending: " + sym
		case c.Price <= 0:
			e.Reason = "no price"
		default:
			alloc := c.AllocPct
			if alloc <= 0 || alloc > 1 {
				e.Reason = fmt.Sprintf("no valid allocation (%v): expected a fraction of equity such as 0.20", alloc)
			} else if qty := math.Floor(equity * alloc / c.Price); qty >= 1 {
				e.Quantity = qty
				e.Notional = qty * c.Price
				seen[sym] = true
			} else {
				e.Reason = "less than one share at the strategy's position size"
			}
		}
		out = append(out, e)
	}
	return out
}

// SessionMonth is the YYYY-MM of the session an entry is for: the signal's next
// session, or the New York month of now when the signal has none.
func SessionMonth(nextSession string, now time.Time) string {
	if len(nextSession) >= 7 {
		return nextSession[:7]
	}
	return now.In(job_runs.EasternTime()).Format("2006-01")
}

// FilledThisMonth maps "strategy|SYMBOL" to true for every position the account
// opened (an entry that filled) in the New York calendar month month (YYYY-MM). It reads
// manage_exit, which records a position when its entry fills, whatever has happened
// to it since. A strategy that takes a name out of the rotation after a fill
// (StrategyConfig.OneFillPerMonth) skips these until the next month.
func FilledThisMonth(db *sql.DB, accNum, month string) map[string]bool {
	out := map[string]bool{}
	if db == nil {
		return out
	}
	rows, err := db.Query(orchestratorsql.Q("stage_orders/filled_this_month"), accNum, accNum)
	if err != nil {
		return out // no manage_exit table yet: nothing has filled
	}
	defer rows.Close()
	et := job_runs.EasternTime()
	for rows.Next() {
		var name, id, sym string
		var at any
		if rows.Scan(&name, &id, &sym, &at) != nil {
			continue
		}
		t := parseDBTime(at)
		if t.IsZero() || t.In(et).Format("2006-01") != month {
			continue
		}
		sym = strings.ToUpper(strings.TrimSpace(sym))
		for _, s := range []string{name, id} {
			if s = strings.TrimSpace(s); s != "" {
				out[s+"|"+sym] = true
			}
		}
	}
	return out
}

// parseDBTime reads a DATETIME column however the driver returned it.
func parseDBTime(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if t, err := time.Parse(layout, strings.TrimSpace(x)); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

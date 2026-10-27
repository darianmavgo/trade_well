// Package backfill_fills corrects closed manage_exit rows that were recorded without the broker's
// fill prices: it reads each account's orders from Schwab and gives the rows their real entry and
// exit fills, then re-measures their P&L from them. It places and cancels nothing. manage_exits does
// the same correction on every run; this runs it on its own, so the rows can be fixed on demand
// without the exit manager, which may sell.
package backfill_fills

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_status"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

// JobName is the CLI subcommand and the log prefix.
const JobName = "backfill_fills"

// Run corrects the closed rows of every account live read. With dryRun it reports what it would
// change and writes nothing. It returns one line per row, and one per account Schwab could not
// be read for: a row is never touched on the strength of an account whose orders are missing.
func Run(db *sql.DB, live *strategy_status.Live, dryRun bool) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("backfill_fills: nil db")
	}
	if err := manage_exits.EnsureSchema(db); err != nil {
		return nil, err
	}
	var accounts []string
	for a := range live.Accounts {
		accounts = append(accounts, a)
	}
	sort.Strings(accounts)
	var lines []string
	for _, e := range live.Errors {
		lines = append(lines, "not read: "+e)
	}
	for _, a := range accounts {
		orders := schwab.FlattenOrders(live.Orders[a])
		for _, l := range manage_exits.BackfillFills(db, a, orders, dryRun) {
			lines = append(lines, fmt.Sprintf("account ...%s %s", last4(a), l))
		}
	}
	return lines, nil
}

func last4(s string) string {
	if len(s) > 4 {
		return s[len(s)-4:]
	}
	return s
}

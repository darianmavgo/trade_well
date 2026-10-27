package stage_orders

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// SizedEntry is a scan_market.Signal after leftover-cash sizing. Quantity==0
// means skip (see Reason).
type SizedEntry struct {
	scan_market.Signal
	Quantity float64
	Notional float64
	Reason   string
	// Watch marks a limit entry staged as WATCHING (see WatchEntries): Quantity is its
	// full-size share count and no cash is claimed for it.
	Watch bool
}

// SizeStackEntries allocates one cash ledger across a priority stack.
// Primary (lowest Priority) is sized against full equity first; overlays
// only use leftover cash and never evict an open position (live v1: no
// market-preempt). Already-held symbols are skipped.
func SizeStackEntries(equity, cash float64, held map[string]bool, cands []scan_market.Signal) []SizedEntry {
	ordered := append([]scan_market.Signal(nil), cands...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Priority < ordered[j].Priority
	})
	claimed := make(map[string]bool, len(held)+len(ordered))
	for k, v := range held {
		if v {
			claimed[strings.ToUpper(k)] = true
		}
	}
	remaining := cash
	if remaining < 0 {
		remaining = 0
	}
	if equity <= 0 {
		equity = remaining
	}
	out := make([]SizedEntry, 0, len(ordered))
	for _, c := range ordered {
		e := SizedEntry{Signal: c}
		sym := strings.ToUpper(strings.TrimSpace(c.Symbol))
		if sym == "" {
			e.Reason = "empty symbol"
			out = append(out, e)
			continue
		}
		if claimed[sym] {
			e.Reason = "already holding or pending: " + sym
			out = append(out, e)
			continue
		}
		if c.Price <= 0 {
			e.Reason = "no price"
			out = append(out, e)
			continue
		}
		cap := remaining * 0.95
		var target float64
		switch c.Sizing {
		case "fixed_shares":
			// The strategy's own rule (e.g. the one-share TSLL smoke test): never
			// scale it up to a percentage of equity.
			target = float64(c.FixedShares) * c.Price
			if c.FixedShares <= 0 {
				e.Reason = "fixed_shares sizing with no share count"
				out = append(out, e)
				continue
			}
		case "fixed_dollar":
			target = c.FixedDollar
			if target <= 0 {
				e.Reason = "fixed_dollar sizing with no dollar amount"
				out = append(out, e)
				continue
			}
		default:
			alloc := c.AllocPct
			if alloc <= 0 || alloc > 1 {
				// No guessed size: the backtest this trades sized a known fraction, and a
				// silent default (this used to be 65%) trades something else.
				e.Reason = fmt.Sprintf("no valid allocation (%v): expected a fraction of equity such as 0.20", alloc)
				out = append(out, e)
				continue
			}
			target = equity * alloc
		}
		if target > cap {
			target = cap
		}
		qty := math.Floor(target / c.Price)
		if qty < 1 {
			e.Reason = "insufficient leftover cash"
			out = append(out, e)
			continue
		}
		e.Quantity = qty
		e.Notional = qty * c.Price
		remaining -= e.Notional
		claimed[sym] = true
		out = append(out, e)
	}
	return out
}

// accountCapital returns live equity and cash for %-of-equity sizing. There
// is no synthetic fallback: sizing is driven only by the resolved account's
// real balances. With no resolved account, or one with no reported balance or
// no cash available (a fully invested account), the matching figure comes
// back zero -- SizeStackEntries then sizes every
// candidate to zero ("insufficient leftover cash") rather than trading
// against a guessed number.
func accountCapital(acc *trader.ResolvedAccount) (equity, cash float64) {
	if acc == nil {
		return 0, 0
	}
	equity = acc.Details.CurrentBalances.LiquidationValue
	if equity <= 0 {
		equity = acc.Details.ProjectedBalances.LiquidationValue
	}
	cash = trader.GetBuyingPower(&acc.Details)
	if cash < 0 {
		cash = 0
	}
	return equity, cash
}

// heldSymbols is every symbol the account really holds. The broker snapshot is
// the truth: manage_exit rows are bookkeeping and go stale (a position sold
// by a stop, by hand, or while manage_exits was failing leaves its row OPEN),
// and a stale row must never block a buy. Only when there is no broker
// snapshot at all (acc == nil) does it fall back to open manage_exit rows,
// the best remaining evidence. Entries accepted but not yet visible at the
// broker are covered separately by addPendingStagedSymbols. Queried by raw
// SQL rather than importing pkg/manage_exits, since stage_orders is built
// before manage_exits in the package dependency order.
func heldSymbols(db *sql.DB, acc *trader.ResolvedAccount, accNum string) map[string]bool {
	held := make(map[string]bool)
	if acc == nil {
		if db != nil && accNum != "" {
			rows, err := db.Query(`SELECT symbol FROM manage_exit WHERE status = 'OPEN' AND account_number = ?`, accNum)
			if err == nil {
				for rows.Next() {
					var sym string
					if rows.Scan(&sym) == nil {
						if s := strings.ToUpper(strings.TrimSpace(sym)); s != "" {
							held[s] = true
						}
					}
				}
				rows.Close()
			}
		}
		return held
	}
	for _, p := range acc.Details.Positions {
		if p.LongQuantity <= 0 && p.ShortQuantity <= 0 {
			continue
		}
		if s := strings.ToUpper(strings.TrimSpace(p.Instrument.Symbol)); s != "" {
			held[s] = true
		}
	}
	return held
}

// addPendingStagedSymbols marks a symbol as claimed (mutating held) when
// accNum already has a PENDING stage_order row for it, or one accepted by
// the broker but not yet confirmed filled (StagedOrderPendingFill),
// regardless of which strategy staged it. Without this, a second scan run
// before submit-orders resolves the queue (a retried job, a re-run during
// troubleshooting) sees the symbol as not-yet-held and stages a duplicate
// buy for the same signal.
func addPendingStagedSymbols(db *sql.DB, accNum string, held map[string]bool) {
	if db == nil || accNum == "" {
		return
	}
	pending, err := GetPendingStagedOrders(db, accNum)
	if err == nil {
		for _, p := range pending {
			if s := strings.ToUpper(strings.TrimSpace(p.Symbol)); s != "" {
				held[s] = true
			}
		}
	}
	if watching, err := GetWatchingStagedOrders(db, accNum); err == nil {
		for _, p := range watching {
			if s := strings.ToUpper(strings.TrimSpace(p.Symbol)); s != "" {
				held[s] = true
			}
		}
	}
	awaitingFill, err := GetOrdersAwaitingFillConfirmation(db, accNum)
	if err == nil {
		for _, p := range awaitingFill {
			if s := strings.ToUpper(strings.TrimSpace(p.Symbol)); s != "" {
				held[s] = true
			}
		}
	}
}

// AccountCapital is the live equity and cash a %-of-equity order is sized against
// (zero for both when there is no usable account balance).
func AccountCapital(acc *trader.ResolvedAccount) (equity, cash float64) { return accountCapital(acc) }

// BrokerHeld is every symbol the account really holds (see heldSymbols).
func BrokerHeld(db *sql.DB, acc *trader.ResolvedAccount, accNum string) map[string]bool {
	return heldSymbols(db, acc, accNum)
}

// heldMarketValue is the market value of the long positions acc holds (0 with no account).
func heldMarketValue(acc *trader.ResolvedAccount) float64 {
	if acc == nil {
		return 0
	}
	var v float64
	for _, p := range acc.Details.Positions {
		if p.LongQuantity > 0 {
			v += p.MarketValue
		}
	}
	return v
}

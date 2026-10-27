package risk

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// CheckEntry applies the per-order caps to one buy about to go to the broker. It returns "" when
// the order may go, or the reason it may not. cost is quantity x entry price, equity is the
// account's live equity, ordersToday and openPositions come from Counts. A zero cap is off.
//
// These are fat-finger and runaway limits, not strategy limits: see DefaultLimits.
func (s State) CheckEntry(cost, equity float64, ordersToday, openPositions int) string {
	if s.MaxOrderPct > 0 && equity > 0 && cost > s.MaxOrderPct*equity {
		return fmt.Sprintf("order cost $%.2f is %.0f%% of equity, over the %.0f%% per-order cap", cost, cost/equity*100, s.MaxOrderPct*100)
	}
	if s.MaxOrdersPerDay > 0 && ordersToday >= s.MaxOrdersPerDay {
		return fmt.Sprintf("%d entry orders already placed this session, at the daily cap of %d", ordersToday, s.MaxOrdersPerDay)
	}
	if s.MaxPositions > 0 && openPositions >= s.MaxPositions {
		return fmt.Sprintf("%d open positions, at the cap of %d", openPositions, s.MaxPositions)
	}
	return ""
}

// Counts reads what CheckEntry needs: the entry orders already placed for the account in the
// ET session starting at sessionStart (UTC), and its open long positions in the account mirror.
func Counts(db *sql.DB, account string, sessionStart time.Time) (ordersToday, openPositions int, err error) {
	err = db.QueryRow(orchestratorsql.Q("risk/counts"), account, sessionStart.UTC()).Scan(&ordersToday)
	if err != nil {
		return 0, 0, fmt.Errorf("risk: count today's orders: %w", err)
	}
	err = db.QueryRow(orchestratorsql.Q("risk/counts_2"), account).Scan(&openPositions)
	if err != nil {
		return 0, 0, fmt.Errorf("risk: count open positions: %w", err)
	}
	return ordersToday, openPositions, nil
}

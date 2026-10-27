package scan_market

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// BuyRow is one BUY candidate a scan produced, and what became of it.
type BuyRow struct {
	CreatedAt   string  `json:"created_at"`
	AsOf        string  `json:"as_of"`
	NextSession string  `json:"next_session"`
	StackID     string  `json:"stack_id"`
	StrategyID  string  `json:"strategy_id"`
	Symbol      string  `json:"symbol"`
	Close       float64 `json:"close"`
	BuyLimit    float64 `json:"buy_limit"`
	TakeProfit  float64 `json:"take_profit"`
	StopLoss    float64 `json:"stop_loss"`
	AllocPct    float64 `json:"alloc_pct"`
	HoldDays    int     `json:"hold_days"`
	Status      string  `json:"status"`
	Notes       string  `json:"notes"`
}

// NoBuyRow is a stack member that produced no BUY on a day, with the scan's reason.
type NoBuyRow struct {
	Date     string `json:"date"`
	Strategy string `json:"strategy"`
	Detail   string `json:"detail"`
}

// Results is what the scans of the last Days days found.
type Results struct {
	Days   int        `json:"days"`
	Buys   []BuyRow   `json:"buys"`
	NoBuys []NoBuyRow `json:"no_buys"`
	// Hidden counts the rows left out because their strategy is not in the filter (strategies
	// that were deployed once and no longer are).
	Hidden int `json:"hidden"`
	// Trace is the nightly verdict of each strategy with the reason (why it did or did not
	// enter); Latest is what the newest scan said about each strategy's last five sessions.
	Trace  []TraceRow `json:"trace"`
	Latest []TraceRow `json:"latest_scan"`
}

// RecentFor reads the BUY candidates and NO_BUY notes the scans wrote in the last days days (by
// the date of the bar they evaluated), newest first, only for the strategy ids in ids (nil keeps
// every row). The rows of a stack as a whole carry the stack id and no reason, so they are never
// shown when a filter is given: the members' own rows say it. It only reads.
func RecentFor(db *sql.DB, now time.Time, days int, ids []string) (Results, error) {
	if days < 1 {
		days = 7
	}
	res := Results{Days: days, Buys: []BuyRow{}, NoBuys: []NoBuyRow{}, Trace: []TraceRow{}, Latest: []TraceRow{}} // [] not null in the JSON
	if db == nil {
		return res, fmt.Errorf("scan_market.Recent: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return res, err
	}
	since := now.UTC().AddDate(0, 0, -days).Format("2006-01-02")

	rows, err := db.Query(orchestratorsql.Q("scan_market/recent_for"), since)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BuyRow
		if err := rows.Scan(&b.CreatedAt, &b.AsOf, &b.NextSession, &b.StackID, &b.StrategyID, &b.Symbol, &b.Close, &b.BuyLimit,
			&b.TakeProfit, &b.StopLoss, &b.AllocPct, &b.HoldDays, &b.Status, &b.Notes); err != nil {
			return res, err
		}
		if keep(ids, b.StrategyID) {
			res.Buys = append(res.Buys, b)
		} else {
			res.Hidden++
		}
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	nb, err := db.Query(orchestratorsql.Q("scan_market/recent_for_2"), since)
	if err != nil {
		return res, err
	}
	defer nb.Close()
	for nb.Next() {
		var n NoBuyRow
		if err := nb.Scan(&n.Date, &n.Strategy, &n.Detail); err != nil {
			return res, err
		}
		if keep(ids, n.Strategy) {
			res.NoBuys = append(res.NoBuys, n)
		} else {
			res.Hidden++
		}
	}
	if err := nb.Err(); err != nil {
		return res, err
	}
	if res.Trace, err = RecentTrace(db, now, days, ids); err != nil {
		return res, err
	}
	res.Latest, err = LatestTrace(db, ids)
	return res, err
}

func keep(ids []string, id string) bool {
	if ids == nil {
		return true
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

package accountstore

import (
	"database/sql"
	"fmt"
	"strings"
)

// The resting exits of a position, read from Schwab's own orders: profit_taker is the
// price of a working SELL LIMIT and stop_loss the trigger price of a working SELL STOP
// (or STOP_LIMIT) on the symbol. Nothing here is calculated from an entry price or a
// strategy: if Schwab shows no such order, the column is NULL.
//
// With several working exits on one symbol the nearest one wins: the LOWEST limit price
// (the take-profit that fills first) and the HIGHEST stop price (the stop that triggers
// first).

// exitPrices are the resting exits found for one symbol.
type exitPrices struct {
	profitTaker float64
	hasPT       bool
	stopLoss    float64
	hasSL       bool
}

// exitActive reports whether an order in this broker status is live at the broker. An
// exit still AWAITING_PARENT_ORDER belongs to an entry that has not filled, so no
// position carries it yet.
func exitActive(status string) bool {
	switch strings.ToUpper(status) {
	case "WORKING", "PENDING_ACTIVATION", "QUEUED", "ACCEPTED",
		"AWAITING_STOP_CONDITION", "AWAITING_CONDITION", "PENDING_ACKNOWLEDGEMENT":
		return true
	}
	return false
}

// collectExits walks one order as decoded from Schwab's JSON, and every child order
// beneath it (the OCO legs of a filled bracket are children of the FILLED entry), and
// records each live SELL limit/stop per symbol into out.
func collectExits(ord map[string]interface{}, out map[string]*exitPrices) {
	status, _ := ord["status"].(string)
	if exitActive(status) {
		orderType, _ := ord["orderType"].(string)
		price, _ := ord["price"].(float64)
		stop, _ := ord["stopPrice"].(float64)
		if legs, ok := ord["orderLegCollection"].([]interface{}); ok {
			for _, l := range legs {
				leg, ok := l.(map[string]interface{})
				if !ok {
					continue
				}
				if instr, _ := leg["instruction"].(string); !strings.EqualFold(instr, "SELL") {
					continue // a buy, or SELL_SHORT: not an exit of a long position
				}
				inst, _ := leg["instrument"].(map[string]interface{})
				symbol, _ := inst["symbol"].(string)
				if symbol == "" {
					continue
				}
				e := out[symbol]
				if e == nil {
					e = &exitPrices{}
					out[symbol] = e
				}
				switch strings.ToUpper(orderType) {
				case "LIMIT":
					if price > 0 && (!e.hasPT || price < e.profitTaker) {
						e.profitTaker, e.hasPT = price, true
					}
				case "STOP", "STOP_LIMIT":
					if stop > 0 && (!e.hasSL || stop > e.stopLoss) {
						e.stopLoss, e.hasSL = stop, true
					}
				}
			}
		}
	}
	if kids, ok := ord["childOrderStrategies"].([]interface{}); ok {
		for _, k := range kids {
			if kid, ok := k.(map[string]interface{}); ok {
				collectExits(kid, out)
			}
		}
	}
}

// applyExits writes the exits found per account onto the position rows. Every account in
// found is cleared first, so an exit that was cancelled since the last sync disappears
// instead of lingering. A symbol with no position row is ignored.
func (s *Store) applyExits(db Execer, found map[string]map[string]*exitPrices) {
	for acc, bySymbol := range found {
		_, _ = db.Exec(s.q(`UPDATE {position} SET profit_taker = NULL, stop_loss = NULL WHERE account_number = ?`), acc)
		for symbol, e := range bySymbol {
			var pt, sl any
			if e.hasPT {
				pt = e.profitTaker
			}
			if e.hasSL {
				sl = e.stopLoss
			}
			if pt == nil && sl == nil {
				continue
			}
			_, _ = db.Exec(s.q(`UPDATE {position} SET profit_taker = ?, stop_loss = ? WHERE account_number = ? AND symbol = ?`), pt, sl, acc, symbol)
		}
	}
}

// SetPositionExitDates records a planned exit date ("2006-01-02") on positions. Unlike
// profit_taker and stop_loss it is not read from Schwab: a broker knows nothing about a
// strategy's hold window, so the caller that does (trade_orchestrator, from its managed
// trades) supplies it. Every account in accounts is cleared first, so a date whose trade
// has closed disappears; dates maps account -> symbol -> date, and a symbol with no
// position row is ignored.
func (s *Store) SetPositionExitDates(db Execer, accounts []string, dates map[string]map[string]string) {
	for _, acc := range accounts {
		_, _ = db.Exec(s.q(`UPDATE {position} SET exit_date = NULL WHERE account_number = ?`), acc)
		for symbol, date := range dates[acc] {
			if date == "" {
				continue
			}
			_, _ = db.Exec(s.q(`UPDATE {position} SET exit_date = ? WHERE account_number = ? AND symbol = ?`), date, acc, symbol)
		}
	}
}

// Querier is the read half of *sql.DB and *sql.Tx.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// PositionExitsTable returns every stored position as the Positions table the account_state
// page shows: the columns it always had, plus the resting profit_taker and stop_loss read
// from Schwab's working orders, and the planned exit_date the caller supplied through
// SetPositionExitDates. Call it after positions and orders have been parsed; the exits are
// only known once the orders are.
func (s *Store) PositionExitsTable(db Querier) (*Table, error) {
	rows, err := db.Query(s.q(`SELECT account_number, symbol, asset_type, long_quantity, short_quantity, average_price,
		market_value, current_day_pl, profit_taker, stop_loss, exit_date
		FROM {position} ORDER BY account_number, symbol`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	t := &Table{
		Name:  s.t.Position,
		Label: "Positions",
		Columns: []string{"account_number", "symbol", "asset_type", "long_quantity", "short_quantity", "average_price",
			"market_value", "current_day_pl", "profit_taker", "stop_loss", "exit_date"},
	}
	price := func(v sql.NullFloat64) string {
		if !v.Valid {
			return "none"
		}
		return fmt.Sprintf("%.2f", v.Float64)
	}
	for rows.Next() {
		var acc, sym string
		var asset sql.NullString
		var long, short, avg, mv, dayPL, pt, sl sql.NullFloat64
		var exit sql.NullString
		if err := rows.Scan(&acc, &sym, &asset, &long, &short, &avg, &mv, &dayPL, &pt, &sl, &exit); err != nil {
			return nil, err
		}
		t.Rows = append(t.Rows, []string{acc, sym, asset.String, fmt.Sprintf("%g", long.Float64), fmt.Sprintf("%g", short.Float64),
			fmt.Sprintf("%.4f", avg.Float64), fmt.Sprintf("%.2f", mv.Float64), fmt.Sprintf("%.2f", dayPL.Float64), price(pt), price(sl), dateOrNone(exit)})
	}
	return t, rows.Err()
}

func dateOrNone(v sql.NullString) string {
	if !v.Valid || v.String == "" {
		return "none"
	}
	return v.String
}

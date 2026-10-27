package live_vs_backtest_report

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/accountstore"
)

// execFill is one order's own execution. A bracket sell nested under the
// entry keeps the entry's id in ParentID.
type execFill struct {
	Account, Symbol, Side, OrderID, ParentID string
	Price, Qty                               float64
	At                                       string
}

type exitInfo struct {
	id       int64
	strategy string
	status   string
	account  string
	symbol   string
}

type roundTrip struct {
	account, strategy, symbol string
	greenlit                  int
	buyID, sellID             string
	buyPx, sellPx, qty, fees  float64
	cash, ret                 sql.NullFloat64
	opened, closed, status    string
	exitID                    int64
}

// brokerCash pairs buy and sell executions from the account mirror.
// Cash is the sum of trade net amounts on those order ids when both
// transactions are present. Otherwise it is (sell - buy) * qty, minus fees
// only in that fallback, because a Schwab net amount already includes fees.
func brokerCash(db *sql.DB, allowlist []string, account string) ([]roundTrip, []string, error) {
	fills, err := loadFills(db, account)
	if err != nil {
		return nil, nil, err
	}
	exits, err := loadExits(db, account)
	if err != nil {
		return nil, nil, err
	}
	nets, fees, err := loadTradeCash(db, account)
	if err != nil {
		return nil, nil, err
	}
	trips := pairTrips(fills, exits, nets, fees, allowlist)
	var notes []string
	var unmanaged, openSold int
	for _, t := range trips {
		if t.exitID == 0 && t.sellID != "" {
			unmanaged++
		}
		if t.sellID != "" && (t.status == "OPEN" || strings.HasPrefix(t.status, "PENDING")) {
			openSold++
		}
	}
	if unmanaged > 0 {
		notes = append(notes, fmt.Sprintf("%d filled round trip(s) have no manage_exit row, so live_vs_backtest_trades did not replay them", unmanaged))
	}
	if openSold > 0 {
		notes = append(notes, fmt.Sprintf("%d managed trade(s) are still open in manage_exit after the broker sold them", openSold))
	}
	return trips, notes, nil
}

func loadFills(db *sql.DB, account string) ([]execFill, error) {
	table := account_state.MirrorStore().Tables().Order
	q := `SELECT order_id, account_number, COALESCE(raw_json, '') FROM ` + table
	var args []any
	if account != "" {
		q += ` WHERE account_number = ?`
		args = append(args, account)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []execFill
	for rows.Next() {
		var id, acct, raw string
		if err := rows.Scan(&id, &acct, &raw); err != nil {
			return nil, err
		}
		out = append(out, walkOrder(raw, acct, id)...)
	}
	return out, rows.Err()
}

func walkOrder(raw, account, fallbackID string) []execFill {
	var order map[string]any
	if json.Unmarshal([]byte(raw), &order) != nil {
		return nil
	}
	if idString(order["orderId"]) == "" && fallbackID != "" {
		order["orderId"] = fallbackID
	}
	var out []execFill
	walk(order, account, "", &out)
	return out
}

func walk(order map[string]any, account, root string, out *[]execFill) {
	id := idString(order["orderId"])
	if root == "" {
		root = id
	}
	side, sym := legSideSymbol(order)
	if px, qty, ok := accountstore.FillPrice(order, ""); ok && qty > 0 && sym != "" && (side == "BUY" || side == "SELL") {
		parent := ""
		if id != root {
			parent = root
		}
		*out = append(*out, execFill{
			Account: account, Symbol: sym, Side: side,
			OrderID: id, ParentID: parent,
			Price: px, Qty: qty, At: when(order),
		})
	}
	kids, _ := order["childOrderStrategies"].([]any)
	for _, k := range kids {
		km, ok := k.(map[string]any)
		if !ok {
			continue
		}
		walk(km, account, root, out)
	}
}

func legSideSymbol(order map[string]any) (side, symbol string) {
	legs, _ := order["orderLegCollection"].([]any)
	for _, l := range legs {
		lm, ok := l.(map[string]any)
		if !ok {
			continue
		}
		inst, _ := lm["instruction"].(string)
		sym := ""
		if im, ok := lm["instrument"].(map[string]any); ok {
			sym, _ = im["symbol"].(string)
		}
		switch strings.ToUpper(strings.TrimSpace(inst)) {
		case "BUY", "SELL":
			return strings.ToUpper(strings.TrimSpace(inst)), strings.ToUpper(strings.TrimSpace(sym))
		}
	}
	return "", ""
}

func when(order map[string]any) string {
	for _, k := range []string{"closeTime", "enteredTime"} {
		if s, _ := order[k].(string); strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func idString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func loadExits(db *sql.DB, account string) (map[string]exitInfo, error) {
	q := `SELECT id, account_number, symbol,
		CASE WHEN COALESCE(strategy_id, '') != '' THEN strategy_id ELSE strategy_name END,
		COALESCE(entry_order_id, ''), status
		FROM manage_exit`
	var args []any
	if account != "" {
		q += ` WHERE account_number = ?`
		args = append(args, account)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]exitInfo{}
	for rows.Next() {
		var e exitInfo
		var orderID string
		if err := rows.Scan(&e.id, &e.account, &e.symbol, &e.strategy, &orderID, &e.status); err != nil {
			return nil, err
		}
		e.symbol = strings.ToUpper(e.symbol)
		if orderID != "" {
			out[orderID] = e
		}
	}
	return out, rows.Err()
}

func loadTradeCash(db *sql.DB, account string) (nets, fees map[string]float64, err error) {
	table := account_state.MirrorStore().Tables().Transaction
	q := `SELECT account_number, COALESCE(transaction_type, ''), COALESCE(net_amount, 0), COALESCE(fees, 0), COALESCE(raw_json, '')
		FROM ` + table
	var args []any
	if account != "" {
		q += ` WHERE account_number = ?`
		args = append(args, account)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	nets = map[string]float64{}
	fees = map[string]float64{}
	for rows.Next() {
		var acct, kind, raw string
		var net, fee float64
		if err := rows.Scan(&acct, &kind, &net, &fee, &raw); err != nil {
			return nil, nil, err
		}
		if kind != "" && kind != "TRADE" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		id := idString(m["orderId"])
		if id == "" {
			continue
		}
		key := acct + "|" + id
		nets[key] += net
		fees[key] += fee
	}
	return nets, fees, rows.Err()
}

func pairTrips(fills []execFill, exits map[string]exitInfo, nets, fees map[string]float64, allowlist []string) []roundTrip {
	sort.SliceStable(fills, func(i, j int) bool {
		if fills[i].At != fills[j].At {
			return fills[i].At < fills[j].At
		}
		if fills[i].Side != fills[j].Side {
			return fills[i].Side == "BUY"
		}
		return fills[i].OrderID < fills[j].OrderID
	})
	var trips []roundTrip
	open := map[string][]int{}
	for _, f := range fills {
		key := f.Account + "|" + f.Symbol
		if f.Side == "BUY" {
			t := roundTrip{
				account: f.Account, symbol: f.Symbol,
				buyID: f.OrderID, buyPx: f.Price, qty: f.Qty, opened: f.At,
			}
			if e, ok := exits[f.OrderID]; ok {
				t.strategy = e.strategy
				t.exitID = e.id
				t.status = e.status
			}
			if t.strategy != "" && strategies.MemberGreenlit(allowlist, t.strategy) {
				t.greenlit = 1
			}
			trips = append(trips, t)
			open[key] = append(open[key], len(trips)-1)
			continue
		}
		chosen := -1
		for _, i := range open[key] {
			if f.ParentID != "" && trips[i].buyID == f.ParentID {
				chosen = i
				break
			}
		}
		if chosen < 0 && len(open[key]) > 0 {
			chosen = open[key][0]
		}
		if chosen < 0 {
			continue
		}
		t := &trips[chosen]
		t.sellID = f.OrderID
		t.sellPx = f.Price
		t.closed = f.At
		if f.Qty < t.qty {
			t.qty = f.Qty
		}
		applyCash(t, nets, fees)
		open[key] = removeIndex(open[key], chosen)
	}
	return trips
}

func applyCash(t *roundTrip, nets, fees map[string]float64) {
	buyKey := t.account + "|" + t.buyID
	sellKey := t.account + "|" + t.sellID
	buyNet, buyOK := nets[buyKey]
	sellNet, sellOK := nets[sellKey]
	t.fees = fees[buyKey] + fees[sellKey]
	if buyOK && sellOK {
		t.cash = sql.NullFloat64{Float64: buyNet + sellNet, Valid: true}
		if buyNet != 0 {
			den := buyNet
			if den < 0 {
				den = -den
			}
			t.ret = sql.NullFloat64{Float64: t.cash.Float64 / den, Valid: true}
		}
		return
	}
	if t.buyPx > 0 && t.sellID != "" {
		cash := (t.sellPx-t.buyPx)*t.qty - t.fees
		t.cash = sql.NullFloat64{Float64: cash, Valid: true}
		t.ret = sql.NullFloat64{Float64: (t.sellPx - t.buyPx) / t.buyPx, Valid: true}
	}
}

func removeIndex(xs []int, v int) []int {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

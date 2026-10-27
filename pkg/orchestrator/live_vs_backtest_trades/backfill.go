package live_vs_backtest_trades

import (
	"database/sql"
	"encoding/json"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/schwaber/accountstore"
)

// backfillFills copies broker execution prices and fees onto manage_exit, and
// fills in a decision key when the stage and the buy signal still have it.
// Rows that already have a price are left alone.
func backfillFills(db *sql.DB) error {
	mirror := account_state.MirrorStore()
	if err := mirror.EnsureSchema(db); err != nil {
		return err
	}
	orders := mirror.Tables().Order
	txns := mirror.Tables().Transaction
	rows, err := db.Query(`SELECT me.id, o.raw_json FROM manage_exit me
		JOIN ` + quote(orders) + ` o ON o.order_id = me.entry_order_id
		WHERE me.entry_order_id != '' AND o.raw_json IS NOT NULL AND o.raw_json != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type hit struct {
		id  int64
		raw string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.raw); err != nil {
			return err
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range hits {
		var order map[string]interface{}
		if json.Unmarshal([]byte(h.raw), &order) != nil {
			continue
		}
		buy, qty, buyOK := accountstore.FillPrice(order, "BUY")
		sell, _, sellOK := accountstore.FillPrice(order, "SELL")
		if buyOK {
			if _, err := db.Exec(`UPDATE manage_exit SET entry_fill_price = ?, filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
				WHERE id = ? AND entry_fill_price = 0`, buy, qty, qty, h.id); err != nil {
				return err
			}
		}
		if sellOK {
			if _, err := db.Exec(`UPDATE manage_exit SET exit_fill_price = ? WHERE id = ? AND exit_fill_price = 0`, sell, h.id); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(`UPDATE manage_exit SET fees = (
		SELECT COALESCE(SUM(fees), 0) FROM ` + quote(txns) + ` t
		WHERE t.account_number = manage_exit.account_number AND t.symbol = manage_exit.symbol
		AND (
			substr(t.trade_date, 1, 10) = substr(COALESCE(manage_exit.entry_filled_at, manage_exit.entry_time), 1, 10)
			OR (manage_exit.exit_date != '' AND substr(t.trade_date, 1, 10) = substr(manage_exit.exit_date, 1, 10))
		)
	) WHERE EXISTS (
		SELECT 1 FROM ` + quote(txns) + ` t
		WHERE t.account_number = manage_exit.account_number AND t.symbol = manage_exit.symbol
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE manage_exit SET stage_order_id = (
		SELECT id FROM stage_order WHERE order_id = manage_exit.entry_order_id ORDER BY id DESC LIMIT 1
	) WHERE stage_order_id = 0 AND entry_order_id != ''
		AND EXISTS (SELECT 1 FROM stage_order WHERE order_id = manage_exit.entry_order_id)`); err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE manage_exit SET
		buy_signal_id = COALESCE((SELECT id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), 0),
		strategy_id = CASE WHEN strategy_id = '' THEN COALESCE((SELECT strategy_id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), strategy_name) ELSE strategy_id END,
		as_of_date = CASE WHEN as_of_date = '' THEN COALESCE((SELECT as_of_date FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE as_of_date END,
		next_session = CASE WHEN next_session = '' THEN COALESCE((SELECT next_session FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE next_session END
		WHERE stage_order_id > 0 AND (buy_signal_id = 0 OR strategy_id = '' OR as_of_date = '')`)
	return err
}

func quote(ident string) string {
	return `"` + ident + `"`
}

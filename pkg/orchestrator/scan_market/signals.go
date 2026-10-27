package scan_market

import (
	"database/sql"
	"fmt"
	"time"
)

// UpsertSignalDay writes exactly one row per (strategy, trading day) to
// scan_market_signal. Exported because pkg/stage_orders and pkg/submit_orders
// also record/advance signal status against this table (staged, executed).
func UpsertSignalDay(db *sql.DB, strategyName, signalDate, symbol, signalType string, price float64, detail, status string, generatedAt time.Time) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("nil db")
	}
	if signalDate == "" {
		signalDate = generatedAt.UTC().Format("2006-01-02")
	}
	_, err := db.Exec(`
		INSERT INTO scan_market_signal (symbol, strategy_name, signal_type, price, indicator_data, generated_at, status, signal_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(strategy_name, signal_date) DO UPDATE SET
			symbol=excluded.symbol,
			signal_type=excluded.signal_type,
			price=excluded.price,
			indicator_data=excluded.indicator_data,
			generated_at=excluded.generated_at,
			status=excluded.status`,
		symbol, strategyName, signalType, price, detail, generatedAt, status, signalDate)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRow(`SELECT id FROM scan_market_signal WHERE strategy_name = ? AND signal_date = ?`,
		strategyName, signalDate).Scan(&id)
	return id, err
}

// UpdateSignalStatus advances an existing scan_market_signal row's status by
// its numeric id -- used by pkg/stage_orders and pkg/submit_orders, which
// only carry the FK (stage_order.signal_id), not the (strategy, date) key
// UpsertSignalDay keys off of.
func UpdateSignalStatus(db *sql.DB, id int64, status string) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	if id <= 0 {
		return nil
	}
	_, err := db.Exec(`UPDATE scan_market_signal SET status = ? WHERE id = ?`, status, id)
	return err
}

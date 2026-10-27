package scan_market

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// Buy-signal review statuses. Evening-scan writes NEW only; staging is a
// later job that flips rows to STAGED / DISMISSED after human or auto review.
const (
	BuySignalNew       = "NEW"
	BuySignalStaged    = "STAGED"
	BuySignalDismissed = "DISMISSED"
)

// BuySignal is one livescan ENTER captured for review before stage_order.
// No account sizing and no Schwab fields -- pure signal metadata.
type BuySignal struct {
	ID            int64
	ScanRunID     string
	StackID       string
	StrategyID    string
	StrategyName  string
	Symbol        string
	Direction     string
	AsOfDate      string
	NextSession   string
	Price         float64
	Close         float64
	BuyLimit      float64
	TakeProfit    float64
	StopLoss      float64
	AllocationPct float64
	HoldDays      int
	StackPriority int
	Status        string
	StagedOrderID sql.NullInt64
	RawJSON       string
	Notes         string
	CreatedAt     time.Time
}

// InsertBuySignal appends one review row (history kept across re-runs).
func InsertBuySignal(db *sql.DB, s *BuySignal) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("InsertBuySignal: nil db")
	}
	if s.Status == "" {
		s.Status = BuySignalNew
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	var staged any
	if s.StagedOrderID.Valid {
		staged = s.StagedOrderID.Int64
	}
	res, err := db.Exec(orchestratorsql.Q("scan_market/insert_buy_signal"),
		s.ScanRunID, s.StackID, s.StrategyID, s.StrategyName, s.Symbol, s.Direction,
		s.AsOfDate, s.NextSession, s.Price, s.Close, s.BuyLimit, s.TakeProfit, s.StopLoss,
		s.AllocationPct, s.HoldDays, s.StackPriority, s.Status, staged,
		s.RawJSON, s.Notes, s.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MarshalBuySignalRaw stores the livescan signal as JSON for dashboard review.
func MarshalBuySignalRaw(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

// PendingBuySignals returns the NEW buy signals of the most recent scan run
// that still has any, in stack priority order. Older NEW rows belong to
// superseded scans and are returned separately so the caller can dismiss them.
func PendingBuySignals(db *sql.DB) (latest, superseded []BuySignal, err error) {
	if db == nil {
		return nil, nil, fmt.Errorf("PendingBuySignals: nil db")
	}
	rows, err := db.Query(orchestratorsql.Q("scan_market/pending_buy_signals"), BuySignalNew)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var newestRun string
	for rows.Next() {
		var s BuySignal
		var created string
		if err := rows.Scan(
			&s.ID, &s.ScanRunID, &s.StackID, &s.StrategyID, &s.StrategyName, &s.Symbol, &s.Direction,
			&s.AsOfDate, &s.NextSession, &s.Price, &s.Close, &s.BuyLimit, &s.TakeProfit, &s.StopLoss,
			&s.AllocationPct, &s.HoldDays, &s.StackPriority, &s.Status, &s.StagedOrderID,
			&s.RawJSON, &s.Notes, &created,
		); err != nil {
			return nil, nil, err
		}
		if t, perr := time.Parse(time.RFC3339, created); perr == nil {
			s.CreatedAt = t
		}
		if newestRun == "" {
			newestRun = s.ScanRunID
		}
		if s.ScanRunID == newestRun {
			latest = append(latest, s)
		} else {
			superseded = append(superseded, s)
		}
	}
	return latest, superseded, rows.Err()
}

// SetBuySignalStatus moves one buy signal to status (STAGED / DISMISSED),
// optionally linking the stage_order it produced, and appends note.
func SetBuySignalStatus(db *sql.DB, id int64, status string, stagedOrderID int64, note string) error {
	var staged any
	if stagedOrderID > 0 {
		staged = stagedOrderID
	}
	_, err := db.Exec(orchestratorsql.Q("scan_market/set_buy_signal_status"), status, staged, note, note, note, id)
	return err
}

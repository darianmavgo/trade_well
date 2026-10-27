// Package manage_exits owns the open-position lifecycle: pkg/submit_orders
// calls Open on a confirmed fill instead of writing manage_exit itself, and
// Run closes positions on time expiry, take-profit, or stop-loss.
package manage_exits

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TradeStatus represents the state of an automated trade.
const (
	TradeStatusOpen             = "OPEN"
	TradeStatusClosedProfit     = "CLOSED_PROFIT"
	TradeStatusClosedTimeExpiry = "CLOSED_TIME_EXPIRATION"
	TradeStatusClosedStopLoss   = "CLOSED_STOP_LOSS"
	TradeStatusClosedManual     = "CLOSED_MANUAL"
	TradeStatusCancelled        = "CANCELLED"
	// TradeStatusPendingFill: the entry order was accepted by the broker but is not
	// confirmed filled. The row carries a provisional exit date; it is not OPEN, so
	// nothing treats it as a held position until the fill is confirmed.
	TradeStatusPendingFill = "PENDING_FILL"
)

// Trade records a live strategy-managed position (the manage_exit table).
type Trade struct {
	ID                int64      `json:"id"`
	AccountNumber     string     `json:"account_number"`
	Symbol            string     `json:"symbol"`
	StrategyName      string     `json:"strategy_name"`
	EntryOrderID      string     `json:"entry_order_id"`
	ExitOrderID       string     `json:"exit_order_id"`
	EntryPrice        float64    `json:"entry_price"`
	TargetProfitPrice float64    `json:"target_profit_price"`
	StopLossPrice     float64    `json:"stop_loss_price"`
	Quantity          float64    `json:"quantity"`
	EntryTime         time.Time  `json:"entry_time"`
	MaxTradingDays    int        `json:"max_trading_days"`
	TradingDaysHeld   int        `json:"trading_days_held"`
	Status            string     `json:"status"`
	ExitReason        string     `json:"exit_reason"`
	ExitTime          *time.Time `json:"exit_time,omitempty"`
	ExitPrice         float64    `json:"exit_price"`
	PnL               float64    `json:"pnl"`
	PnLPct            float64    `json:"pnl_pct"`
	// ExitDate is the last date (New York, "2006-01-02") the position may be held:
	// max_trading_days trading days after the entry fill date. ExitDateSource says
	// whether it came from the real fill (ExitDateFill) or is still an estimate
	// (ExitDateProvisional). EntryFilledAt is the broker's fill time, nil until known.
	ExitDate       string     `json:"exit_date"`
	ExitDateSource string     `json:"exit_date_source"`
	EntryFilledAt  *time.Time `json:"entry_filled_at,omitempty"`
	// Decision key copied from the scan that staged the order, plus the broker
	// fill beside the limit in EntryPrice. ExitReasonCode is the canonical
	// reason (PROFIT_TARGET, STOP_LOSS, TIME_UP, MANUAL, UNFILLED, ORDER_FAILED).
	StrategyID     string  `json:"strategy_id"`
	AsOfDate       string  `json:"as_of_date"`
	NextSession    string  `json:"next_session"`
	SignalID       int64   `json:"signal_id"`
	BuySignalID    int64   `json:"buy_signal_id"`
	StageOrderID   int64   `json:"stage_order_id"`
	EntryFillPrice float64 `json:"entry_fill_price"`
	ExitFillPrice  float64 `json:"exit_fill_price"`
	FilledQty      float64 `json:"filled_qty"`
	Fees           float64 `json:"fees"`
	ExitReasonCode string  `json:"exit_reason_code"`
}

// OpenTradeParams carries what Open needs to register a newly filled
// position. Callers (pkg/submit_orders) never insert into manage_exit
// directly -- trade-lifecycle ownership stays in this package.
type OpenTradeParams struct {
	AccountNumber     string
	Symbol            string
	StrategyName      string
	EntryOrderID      string
	EntryPrice        float64
	TargetProfitPrice float64
	StopLossPrice     float64
	Quantity          float64
	// EntryTime is when the position opened as far as the caller knows (the fill
	// time when FilledAt is set, otherwise when the order was submitted or noticed).
	EntryTime time.Time
	// FilledAt is the broker's fill time for the entry order; zero when the broker
	// gave none. A non-zero value makes the exit date final (ExitDateFill).
	FilledAt       time.Time
	MaxTradingDays int
	// Decision key and the broker fill. EntryPrice stays the limit that was sent.
	// Zero values leave whatever the row already stored.
	StrategyID     string
	AsOfDate       string
	NextSession    string
	SignalID       int64
	BuySignalID    int64
	StageOrderID   int64
	EntryFillPrice float64
	FilledQty      float64
}

const tradeColumns = `id, account_number, symbol, strategy_name, entry_order_id, exit_order_id,
	entry_price, target_profit_price, stop_loss_price, quantity,
	entry_time, max_trading_days, trading_days_held, status, exit_reason, exit_price, pnl, pnl_pct,
	exit_date, exit_date_source, entry_filled_at,
	strategy_id, as_of_date, next_session, signal_id, buy_signal_id, stage_order_id,
	entry_fill_price, exit_fill_price, filled_qty, fees, exit_reason_code`

func scanTrades(rows *sql.Rows) ([]Trade, error) {
	defer rows.Close()
	var trades []Trade
	for rows.Next() {
		var t Trade
		var exitReason, exitOrderID, entryOrderID sql.NullString
		var filled sql.NullTime
		if err := rows.Scan(
			&t.ID, &t.AccountNumber, &t.Symbol, &t.StrategyName, &entryOrderID, &exitOrderID,
			&t.EntryPrice, &t.TargetProfitPrice, &t.StopLossPrice, &t.Quantity,
			&t.EntryTime, &t.MaxTradingDays, &t.TradingDaysHeld, &t.Status, &exitReason,
			&t.ExitPrice, &t.PnL, &t.PnLPct, &t.ExitDate, &t.ExitDateSource, &filled,
			&t.StrategyID, &t.AsOfDate, &t.NextSession, &t.SignalID, &t.BuySignalID, &t.StageOrderID,
			&t.EntryFillPrice, &t.ExitFillPrice, &t.FilledQty, &t.Fees, &t.ExitReasonCode,
		); err != nil {
			return nil, err
		}
		t.EntryOrderID, t.ExitOrderID, t.ExitReason = entryOrderID.String, exitOrderID.String, exitReason.String
		if filled.Valid {
			ft := filled.Time
			t.EntryFilledAt = &ft
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

// Open registers a newly filled position for exit management. If the entry was
// registered earlier as PENDING_FILL (submit_orders does that when the broker
// accepts the order), that row is promoted to OPEN and its exit date is corrected
// from the fill; otherwise a row is inserted. Either way, a non-zero FilledAt makes
// the exit date final: max_trading_days trading days after the fill date.
func Open(db *sql.DB, params OpenTradeParams) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("manage_exits: database handle is nil")
	}
	if err := EnsureSchema(db); err != nil {
		return 0, fmt.Errorf("manage_exits: ensure schema: %w", err)
	}
	if params.EntryOrderID != "" {
		var id int64
		err := db.QueryRow(`SELECT id FROM manage_exit WHERE account_number = ? AND entry_order_id = ? AND status IN (?, ?) ORDER BY id DESC LIMIT 1`,
			params.AccountNumber, params.EntryOrderID, TradeStatusPendingFill, TradeStatusOpen).Scan(&id)
		if err == nil {
			if err := writeDecision(db, id, params); err != nil {
				return 0, err
			}
			fill := params.FilledAt
			if fill.IsZero() {
				return id, promoteWithoutFill(db, id, params.EntryTime)
			}
			return id, SetFill(db, id, fill)
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
	}
	filled, source, exitDate := any(nil), ExitDateProvisional, ExitDateFor(params.EntryTime, params.MaxTradingDays)
	if !params.FilledAt.IsZero() {
		filled, source, exitDate = params.FilledAt, ExitDateFill, ExitDateFor(params.FilledAt, params.MaxTradingDays)
	}
	args := append([]any{
		params.AccountNumber, params.Symbol, params.StrategyName, params.EntryOrderID, "",
		params.EntryPrice, params.TargetProfitPrice, params.StopLossPrice, params.Quantity,
		params.EntryTime, params.MaxTradingDays, 0, TradeStatusOpen, exitDate, source, filled,
	}, decisionArgs(params)...)
	res, err := db.Exec(insertSQL, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const insertSQL = `
	INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, exit_order_id,
		entry_price, target_profit_price, stop_loss_price, quantity,
		entry_time, max_trading_days, trading_days_held, status,
		exit_date, exit_date_source, entry_filled_at,
		strategy_id, as_of_date, next_session, signal_id, buy_signal_id, stage_order_id,
		entry_fill_price, filled_qty
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func decisionArgs(p OpenTradeParams) []any {
	strategyID := p.StrategyID
	if strategyID == "" {
		strategyID = p.StrategyName
	}
	return []any{strategyID, p.AsOfDate, p.NextSession, p.SignalID, p.BuySignalID, p.StageOrderID, p.EntryFillPrice, p.FilledQty}
}

// writeDecision copies a decision key and a known broker fill onto an existing
// row. Empty strings and zeros leave the stored value alone, so a later fill
// confirmation does not wipe the key recorded when the order was accepted.
func writeDecision(db *sql.DB, id int64, p OpenTradeParams) error {
	strategyID := p.StrategyID
	if strategyID == "" {
		strategyID = p.StrategyName
	}
	_, err := db.Exec(`UPDATE manage_exit SET
		strategy_id = CASE WHEN ? != '' THEN ? ELSE strategy_id END,
		as_of_date = CASE WHEN ? != '' THEN ? ELSE as_of_date END,
		next_session = CASE WHEN ? != '' THEN ? ELSE next_session END,
		signal_id = CASE WHEN ? > 0 THEN ? ELSE signal_id END,
		buy_signal_id = CASE WHEN ? > 0 THEN ? ELSE buy_signal_id END,
		stage_order_id = CASE WHEN ? > 0 THEN ? ELSE stage_order_id END,
		entry_fill_price = CASE WHEN ? > 0 THEN ? ELSE entry_fill_price END,
		filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
		WHERE id = ?`,
		strategyID, strategyID, p.AsOfDate, p.AsOfDate, p.NextSession, p.NextSession,
		p.SignalID, p.SignalID, p.BuySignalID, p.BuySignalID, p.StageOrderID, p.StageOrderID,
		p.EntryFillPrice, p.EntryFillPrice, p.FilledQty, p.FilledQty, id)
	return err
}

// RegisterPending records an entry the broker has accepted but not yet filled, with
// a PROVISIONAL exit date: max_trading_days trading days after the session the order
// is expected to fill on (params.EntryTime is when it was submitted). The row is
// PENDING_FILL, not OPEN, until the fill is confirmed; then SetFill (or Open) turns
// it OPEN and corrects the exit date from the real fill date. It is idempotent by
// (account, entry order id).
func RegisterPending(db *sql.DB, params OpenTradeParams) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("manage_exits: database handle is nil")
	}
	if err := EnsureSchema(db); err != nil {
		return 0, fmt.Errorf("manage_exits: ensure schema: %w", err)
	}
	if params.EntryOrderID == "" {
		return 0, fmt.Errorf("manage_exits: RegisterPending needs the entry order id")
	}
	var id int64
	err := db.QueryRow(`SELECT id FROM manage_exit WHERE account_number = ? AND entry_order_id = ? ORDER BY id DESC LIMIT 1`,
		params.AccountNumber, params.EntryOrderID).Scan(&id)
	if err == nil {
		return id, writeDecision(db, id, params)
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	exitDate := exitDateFromSession(ExpectedFillDate(params.EntryTime), params.MaxTradingDays)
	args := append([]any{
		params.AccountNumber, params.Symbol, params.StrategyName, params.EntryOrderID, "",
		params.EntryPrice, params.TargetProfitPrice, params.StopLossPrice, params.Quantity,
		params.EntryTime, params.MaxTradingDays, 0, TradeStatusPendingFill, exitDate, ExitDateProvisional, nil,
	}, decisionArgs(params)...)
	res, err := db.Exec(insertSQL, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetFill records the entry's real fill time on trade id: status OPEN, entry_time and
// entry_filled_at set to filledAt, and the exit date recomputed as max_trading_days
// trading days after the New York date of the fill (source ExitDateFill). It corrects
// a provisional or wrong date, and is safe to repeat.
func SetFill(db *sql.DB, id int64, filledAt time.Time) error {
	if filledAt.IsZero() {
		return fmt.Errorf("manage_exits: SetFill needs the fill time")
	}
	var maxDays int
	var status string
	if err := db.QueryRow(`SELECT max_trading_days, status FROM manage_exit WHERE id = ?`, id).Scan(&maxDays, &status); err != nil {
		return err
	}
	if status != TradeStatusPendingFill && status != TradeStatusOpen {
		return fmt.Errorf("manage_exits: trade %d is %s; a fill cannot be recorded on it", id, status)
	}
	_, err := db.Exec(`UPDATE manage_exit SET status = ?, entry_time = ?, entry_filled_at = ?, exit_date = ?, exit_date_source = ? WHERE id = ?`,
		TradeStatusOpen, filledAt, filledAt, ExitDateFor(filledAt, maxDays), ExitDateFill, id)
	return err
}

// promoteWithoutFill turns a PENDING_FILL row OPEN when the broker confirmed the fill
// but gave no fill time: the exit date stays provisional, computed from asOf.
func promoteWithoutFill(db *sql.DB, id int64, asOf time.Time) error {
	var maxDays int
	if err := db.QueryRow(`SELECT max_trading_days FROM manage_exit WHERE id = ?`, id).Scan(&maxDays); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE manage_exit SET status = ?, exit_date = CASE WHEN exit_date_source = ? THEN exit_date ELSE ? END WHERE id = ? AND status = ?`,
		TradeStatusOpen, ExitDateFill, ExitDateFor(asOf, maxDays), id, TradeStatusPendingFill)
	return err
}

// CancelPending marks a PENDING_FILL entry CANCELLED: the broker canceled, rejected
// or expired the order without it ever filling, so no position was opened.
func CancelPending(db *sql.DB, id int64, reason string) error {
	_, err := db.Exec(`UPDATE manage_exit SET status = ?, exit_reason = ?, exit_reason_code = ? WHERE id = ? AND status = ?`,
		TradeStatusCancelled, reason, ExitReasonCode(TradeStatusCancelled, reason), id, TradeStatusPendingFill)
	return err
}

// GetPendingFillTrades returns entries accepted but not yet confirmed filled
// (accountNumber "" matches every account).
func GetPendingFillTrades(db *sql.DB, accountNumber string) ([]Trade, error) {
	rows, err := db.Query(`SELECT `+tradeColumns+` FROM manage_exit WHERE status = ? AND (account_number = ? OR ? = '')`,
		TradeStatusPendingFill, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanTrades(rows)
}

// GetOpenTrades returns all positions currently being actively tracked.
func GetOpenTrades(db *sql.DB, accountNumber string) ([]Trade, error) {
	rows, err := db.Query(`SELECT `+tradeColumns+` FROM manage_exit WHERE status = 'OPEN' AND (account_number = ? OR ? = '')`,
		accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanTrades(rows)
}

// CloseActiveTrade marks a trade as closed with performance metrics, from exitPrice, which the
// caller may only know as an order price or an estimate. CloseActiveTradeFilled takes the real fill.
func CloseActiveTrade(db *sql.DB, id int64, status, reason string, exitPrice float64, exitTime time.Time) error {
	return CloseActiveTradeFilled(db, id, status, reason, exitPrice, 0, exitTime)
}

// UpdateTradingDaysHeld records the trading days held counter.
func UpdateTradingDaysHeld(db *sql.DB, id int64, daysHeld int) error {
	_, err := db.Exec("UPDATE manage_exit SET trading_days_held = ? WHERE id = ?", daysHeld, id)
	return err
}

// SetExitOrderID records the broker id of the resting profit-taker (or market exit).
func SetExitOrderID(db *sql.DB, id int64, orderID string) error {
	_, err := db.Exec("UPDATE manage_exit SET exit_order_id = ? WHERE id = ?", orderID, id)
	return err
}

// SetExitDate stores an exit date and where it came from on trade id.
func SetExitDate(db *sql.DB, id int64, exitDate, source string) error {
	_, err := db.Exec("UPDATE manage_exit SET exit_date = ?, exit_date_source = ? WHERE id = ?", exitDate, source, id)
	return err
}

// CancelPendingByOrder cancels the PENDING_FILL row for an entry order, if one exists.
// It reports whether a row was cancelled.
func CancelPendingByOrder(db *sql.DB, accountNumber, entryOrderID, reason string) (bool, error) {
	res, err := db.Exec(`UPDATE manage_exit SET status = ?, exit_reason = ?, exit_reason_code = ? WHERE account_number = ? AND entry_order_id = ? AND status = ?`,
		TradeStatusCancelled, reason, ExitReasonCode(TradeStatusCancelled, reason), accountNumber, entryOrderID, TradeStatusPendingFill)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PlannedExitDates returns, per account and upper-cased symbol, the date ("2006-01-02")
// the OPEN trade on that position must be exited by: its stored exit date (the entry fill
// date plus the strategy's hold window), or for a row from before exit dates existed the
// same calculation from its entry time. With several OPEN trades on one symbol the
// earliest date wins. Positions the pipeline does not manage have no entry.
func PlannedExitDates(db *sql.DB) (map[string]map[string]string, error) {
	trades, err := GetOpenTrades(db, "")
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, t := range trades {
		date := t.ExitDate
		if date == "" && t.MaxTradingDays > 0 {
			date = ExitDateFor(t.EntryTime, t.MaxTradingDays)
		}
		if date == "" {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if out[t.AccountNumber] == nil {
			out[t.AccountNumber] = map[string]string{}
		}
		if cur, ok := out[t.AccountNumber][sym]; !ok || date < cur {
			out[t.AccountNumber][sym] = date
		}
	}
	return out, nil
}

// Package stage_orders sizes scan_market's candidate signals against an
// account's live cash/equity and queues bracket orders for pkg/submit_orders
// to re-validate and place. It needs Schwab (a resolved account balance) and
// strategy code (per-strategy sizing/holding config) -- unlike scan_market,
// which never touches either.
package stage_orders

import "time"

// StagedOrder status values.
const (
	StagedOrderPending   = "PENDING"
	StagedOrderSubmitted = "SUBMITTED"
	StagedOrderRejected  = "REJECTED"
	StagedOrderCancelled = "CANCELLED"
	// StagedOrderSubmitting means a worker has claimed the row and is about
	// to (or may have already) call PlaceOrder. Survives process restart so
	// we reconcile via order intents / broker list instead of blind re-place.
	StagedOrderSubmitting = "SUBMITTING"
	// StagedOrderPendingFill means the broker accepted the order (returned a
	// Location/order ID with no error) but has not yet confirmed it FILLED.
	// Accepted is not the same as filled -- a limit order can sit WORKING
	// indefinitely, or later come back CANCELED / REJECTED / EXPIRED without
	// ever filling. No manage_exit row exists for this order yet; a future
	// submit-orders run re-checks its real status before ever treating it as
	// an open position.
	StagedOrderPendingFill = "SUBMITTED_PENDING_FILL"
	// StagedOrderWatching is a limit entry held back from the broker: a strategy that
	// puts a day limit on many names (more than the account could fund at once) is not
	// placed at the open, because a working buy order reserves its cash. pkg/trigger_limits
	// places it when the day's low reaches the limit, sized against the cash left then,
	// and cancels it at the close if the limit was never reached.
	StagedOrderWatching = "WATCHING"
)

// EntryDurationDay is the entry order duration of a limit that expires at the close.
const EntryDurationDay = "DAY"

// StagedOrder is a bracket order queued by stage_orders for pkg/submit_orders
// to re-validate and submit. Staging decouples signal detection from live
// order placement so submission always checks current account state (cash,
// existing positions) right before it risks real money, rather than trusting
// whatever was true when the scan ran.
type StagedOrder struct {
	ID              int64      `json:"id"`
	SignalID        int64      `json:"signal_id,omitempty"`
	AccountNumber   string     `json:"account_number"`
	StrategyName    string     `json:"strategy_name"`
	Symbol          string     `json:"symbol"`
	Instruction     string     `json:"instruction"` // BUY or SELL
	Quantity        float64    `json:"quantity"`
	EntryPrice      float64    `json:"entry_price"`
	TakeProfitPrice float64    `json:"target_profit_price"`
	StopLossPrice   float64    `json:"stop_loss_price"`
	MaxTradingDays  int        `json:"max_trading_days"`
	Status          string     `json:"status"`
	RejectReason    string     `json:"reject_reason,omitempty"`
	OrderID         string     `json:"order_id,omitempty"`
	ClientOrderID   string     `json:"client_order_id,omitempty"`
	StagedAt        time.Time  `json:"staged_at"`
	SubmittedAt     *time.Time `json:"submitted_at,omitempty"`
	// EntryDuration is the entry order's duration: empty for the default (a limit rests
	// until it fills or is cancelled), EntryDurationDay for a limit that expires at the close.
	EntryDuration string `json:"entry_duration,omitempty"`
	// SessionDate is the session the entry is for (YYYY-MM-DD); empty when not recorded.
	SessionDate string `json:"session_date,omitempty"`
	// AllocPct is the share of equity a WATCHING order is sized at when it triggers.
	AllocPct float64 `json:"alloc_pct,omitempty"`
}

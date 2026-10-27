package manage_exits

import "strings"

// Canonical exit reasons shared with the backtest simulator
// (models.ExitReason). Live-only outcomes have no simulator equivalent.
const (
	ReasonProfitTarget = "PROFIT_TARGET"
	ReasonStopLoss     = "STOP_LOSS"
	ReasonTimeUp       = "TIME_UP"
	ReasonManual       = "MANUAL"
	ReasonUnfilled     = "UNFILLED"
	ReasonOrderFailed  = "ORDER_FAILED"
)

// ExitReasonCode maps a live status and free-text reason onto the canonical code.
// An empty result means the row does not say how it ended.
func ExitReasonCode(status, reason string) string {
	r := strings.ToUpper(reason)
	s := strings.ToUpper(status)
	switch {
	case strings.Contains(r, "PROFIT") || s == TradeStatusClosedProfit:
		return ReasonProfitTarget
	case strings.Contains(r, "STOP") || s == TradeStatusClosedStopLoss:
		return ReasonStopLoss
	case strings.Contains(r, "TIME") || strings.Contains(r, "WINDOW") || strings.Contains(r, "EXPIR") || s == TradeStatusClosedTimeExpiry:
		return ReasonTimeUp
	case strings.Contains(r, "MANUAL") || s == TradeStatusClosedManual:
		return ReasonManual
	case strings.Contains(r, "FAIL") || strings.Contains(r, "REJECT"):
		return ReasonOrderFailed
	case strings.Contains(r, "CANCEL") || strings.Contains(r, "UNFILL") || s == TradeStatusCancelled:
		return ReasonUnfilled
	default:
		return ""
	}
}

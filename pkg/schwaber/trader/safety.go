package trader

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

type ValidationResult struct {
	Valid          bool
	Symbol         string
	Instruction    string
	Quantity       float64
	RefPrice       float64
	EstimatedValue float64
	Warnings       []string
}

// SafetyGuard runs the pre-trade checks (buying power, no short selling).
// There is deliberately no per-order dollar ceiling: order size is bounded by
// the account's buying power and by what the strategies size.
type SafetyGuard struct {
	// A limit price this far from the market (a fraction, 0.5 = 50%) draws a warning; at
	// RejectPct the order is refused. 0 turns that step off.
	WarnPct, RejectPct float64
}

// Price collar defaults. They are wide on purpose: the strategies place limits far from the
// quote, so the collar only refuses a limit more than 2x the market. A price below the market can be at most 100% away, so it can only ever warn.
const (
	DefaultCollarWarnPct   = 0.50
	DefaultCollarRejectPct = 1.00
)

// NewSafetyGuard returns a guard with the default collar, overridden by the environment
// variables PRICE_COLLAR_WARN_PCT and PRICE_COLLAR_REJECT_PCT (fractions; 0 turns a step off).
func NewSafetyGuard() *SafetyGuard {
	return &SafetyGuard{
		WarnPct:   envFraction("PRICE_COLLAR_WARN_PCT", DefaultCollarWarnPct),
		RejectPct: envFraction("PRICE_COLLAR_REJECT_PCT", DefaultCollarRejectPct),
	}
}

// envFraction reads a non-negative fraction from the environment. An unset, empty or invalid
// value gives the default, so a typo in the variable never switches the collar off.
func envFraction(name string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ValidateOrder performs pre-trade risk checks before order submission.
func (sg *SafetyGuard) ValidateOrder(
	order *schwab.Order,
	accDetails *SecuritiesAccount,
	marketPrice float64,
) (*ValidationResult, error) {
	legs, err := ParseOrderLegs(order)
	if err != nil || len(legs) == 0 {
		return nil, fmt.Errorf("invalid order: %w", err)
	}

	leg := legs[0]
	symbol := leg.Instrument.Symbol
	qty := leg.Quantity
	instruction := leg.Instruction

	if qty <= 0 {
		return nil, fmt.Errorf("invalid quantity: %.2f (must be > 0)", qty)
	}

	// Determine reference price
	refPrice := marketPrice
	if order.Price != nil && *order.Price > 0 {
		refPrice = *order.Price
	}

	if refPrice <= 0 && order.OrderType != OrderTypeMarket {
		return nil, fmt.Errorf("could not determine execution price for %s order on %s", order.OrderType, symbol)
	}

	estimatedValue := qty * refPrice

	// 1. Cash Buying Power check for Buy Orders
	if instruction == InstructionBuy && accDetails != nil {
		availableCash := GetBuyingPower(accDetails)
		// 1% buffer for market orders
		multiplier := 1.0
		if order.OrderType == OrderTypeMarket {
			multiplier = 1.01
		}
		requiredCash := estimatedValue * multiplier

		if availableCash <= 0 {
			return nil, fmt.Errorf(
				"no buying power: account reports $%.2f available, cannot buy %s (cost $%.2f)",
				availableCash, symbol, requiredCash,
			)
		}
		if requiredCash > availableCash {
			return nil, fmt.Errorf(
				"insufficient buying power: order cost ($%.2f) exceeds available cash ($%.2f) in this Roth IRA",
				requiredCash, availableCash,
			)
		}
	}

	// 2. Position check for Sell Orders (No short-selling in Roth IRA)
	if instruction == InstructionSell && accDetails != nil {
		var heldQty float64
		for _, pos := range accDetails.Positions {
			if pos.Instrument.Symbol == symbol {
				heldQty = pos.LongQuantity
				break
			}
		}

		if heldQty < qty {
			return nil, fmt.Errorf(
				"cannot execute SELL: You hold %.2f shares of %s, attempted to sell %.2f. Short selling is not allowed in a Roth IRA",
				heldQty, symbol, qty,
			)
		}
	}

	// 4. Price collar: a limit price far from the market warns, and farther still is refused.
	var warnings []string
	if order.OrderType == OrderTypeLimit && marketPrice > 0 && refPrice > 0 {
		diffPct := math.Abs(refPrice-marketPrice) / marketPrice
		if sg.RejectPct > 0 && diffPct > sg.RejectPct {
			return nil, fmt.Errorf(
				"price collar: limit price ($%.2f) is %.1f%% away from current market price ($%.2f), over the %.0f%% limit",
				refPrice, diffPct*100, marketPrice, sg.RejectPct*100,
			)
		}
		if sg.WarnPct > 0 && diffPct > sg.WarnPct {
			warnings = append(warnings, fmt.Sprintf(
				"Limit price ($%.2f) is %.1f%% away from current market price ($%.2f)",
				refPrice, diffPct*100, marketPrice,
			))
		}
	}

	return &ValidationResult{
		Valid:          true,
		Symbol:         symbol,
		Instruction:    instruction,
		Quantity:       qty,
		RefPrice:       refPrice,
		EstimatedValue: estimatedValue,
		Warnings:       warnings,
	}, nil
}

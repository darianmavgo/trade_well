package orderintent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// BrokerOrder is the minimal order shape needed to match intents without
// importing pkg/schwab (avoids a cycle). Callers adapt schwab.Order → BrokerOrder.
type BrokerOrder struct {
	OrderID     string
	Symbol      string
	Instruction string
	Quantity    float64
	EnteredAt   time.Time
	Status      string
}

// OrderLister lists recent orders for an account hash.
type OrderLister interface {
	ListAccountOrdersForReconcile(ctx context.Context, accountHash string, from, to time.Time) ([]BrokerOrder, error)
}

// ReconcileResult summarizes one ReconcilePendingIntents run.
type ReconcileResult struct {
	Checked   int
	Acked     int
	Unchanged int
	Details   []string
}

// ReconcilePendingIntents scans unresolved intents and tries to find matching
// broker orders by symbol/instruction/quantity near created_at. When found,
// the intent is Acked. Schwab does not filter by clientOrderId — this is the
// documented fallback.
func ReconcilePendingIntents(ctx context.Context, store *Store, lister OrderLister) (*ReconcileResult, error) {
	res := &ReconcileResult{}
	if store == nil {
		return res, fmt.Errorf("nil intent store")
	}
	if lister == nil {
		return res, fmt.Errorf("nil order lister")
	}
	unresolved, err := store.ListUnresolved(ctx)
	if err != nil {
		return res, err
	}
	res.Checked = len(unresolved)
	for _, in := range unresolved {
		from := in.CreatedAt.Add(-2 * time.Minute)
		to := in.CreatedAt.Add(24 * time.Hour)
		if to.Before(time.Now().UTC()) {
			to = time.Now().UTC().Add(time.Minute)
		}
		orders, lerr := lister.ListAccountOrdersForReconcile(ctx, in.AccountHash, from, to)
		if lerr != nil {
			res.Details = append(res.Details, fmt.Sprintf("%s: list orders: %v", in.ClientOrderID, lerr))
			res.Unchanged++
			continue
		}
		match := findMatchingOrder(in, orders)
		if match == nil {
			res.Details = append(res.Details, fmt.Sprintf("%s: no broker match yet (%s %s %.0f)", in.ClientOrderID, in.Symbol, in.Instruction, in.Quantity))
			res.Unchanged++
			continue
		}
		if err := store.AckIntent(ctx, in.ClientOrderID, match.OrderID); err != nil {
			res.Details = append(res.Details, fmt.Sprintf("%s: ack failed: %v", in.ClientOrderID, err))
			res.Unchanged++
			continue
		}
		res.Acked++
		res.Details = append(res.Details, fmt.Sprintf("%s: acked broker order %s", in.ClientOrderID, match.OrderID))
	}
	return res, nil
}

func findMatchingOrder(in Intent, orders []BrokerOrder) *BrokerOrder {
	sym := strings.ToUpper(strings.TrimSpace(in.Symbol))
	side := strings.ToUpper(strings.TrimSpace(in.Instruction))
	var best *BrokerOrder
	var bestDelta time.Duration = time.Hour * 48
	for i := range orders {
		o := &orders[i]
		if strings.ToUpper(strings.TrimSpace(o.Symbol)) != sym {
			continue
		}
		if side != "" && strings.ToUpper(strings.TrimSpace(o.Instruction)) != side {
			continue
		}
		if !qtyClose(o.Quantity, in.Quantity) {
			continue
		}
		delta := o.EnteredAt.Sub(in.CreatedAt)
		if delta < 0 {
			delta = -delta
		}
		// Prefer orders entered within 30 minutes of intent creation.
		if delta > 30*time.Minute {
			continue
		}
		if best == nil || delta < bestDelta {
			best = o
			bestDelta = delta
		}
	}
	return best
}

func qtyClose(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) < 1e-6
}

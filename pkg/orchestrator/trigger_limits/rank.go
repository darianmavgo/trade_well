package trigger_limits

import (
	"math"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
)

// swapHysteresis is how much nearer (as a share of price) a watched name must be than a
// resting order before the resting order is swapped out. It keeps two names whose
// distances wobble around each other from being cancelled and replaced every pass.
const swapHysteresis = 0.003

// Price is the price a limit's distance is measured from: the ask, else the last trade
// (before the open there is often no ask).
func (q Quote) Price() float64 {
	if q.Ask > 0 {
		return q.Ask
	}
	return q.Last
}

// Distance is how far price must fall to reach limit, as a share of price: 0.02 means a
// 2% drop fills the order. Zero or negative when the market is already at or through the
// limit. A name with no price is infinitely far, so it is tried last.
func Distance(limit, price float64) float64 {
	if price <= 0 {
		return math.Inf(1)
	}
	return (price - limit) / price
}

// Ranked is a staged order with its distance to fill.
type Ranked struct {
	Order    stage_orders.StagedOrder
	Distance float64
}

// RankByDistance orders staged orders nearest-to-fill first. Names with equal distance
// (or no quote) keep a fixed order by symbol, so repeated passes agree.
func RankByDistance(orders []stage_orders.StagedOrder, quotes map[string]Quote) []Ranked {
	out := make([]Ranked, 0, len(orders))
	for _, o := range orders {
		q := quotes[strings.ToUpper(o.Symbol)]
		out = append(out, Ranked{Order: o, Distance: Distance(o.EntryPrice, q.Price())})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].Order.Symbol < out[j].Order.Symbol
	})
	return out
}

// PlanSwap picks the resting order to give up for a nearer watched name: the farthest
// resting order, when the nearest watched name is closer to filling by more than
// swapHysteresis. ok is false when no swap is worth making. watching must be nearest first.
func PlanSwap(resting, watching []Ranked) (out, in Ranked, ok bool) {
	if len(resting) == 0 || len(watching) == 0 {
		return out, in, false
	}
	out = resting[0]
	for _, r := range resting[1:] {
		if r.Distance > out.Distance {
			out = r
		}
	}
	in = watching[0]
	if math.IsInf(in.Distance, 1) {
		return out, in, false
	}
	return out, in, out.Distance-in.Distance > swapHysteresis
}

// IsBuyingPowerRefusal reports whether a broker (or pre-trade safety) refusal means the
// account has no buying power left for the order, as opposed to a problem with the order
// itself. Matching is on Schwab's text, so the full refusal is always logged and
// reported: when Schwab words it differently this is where to add the phrase.
func IsBuyingPowerRefusal(reason string) bool {
	s := strings.ToLower(reason)
	for _, k := range []string{"buying power", "insufficient fund", "insufficient cash", "not enough cash", "not enough buying", "exceeds available"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

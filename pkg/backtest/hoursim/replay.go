package hoursim

import (
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sim"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// Round-trip outcomes from ReplayRoundTrip. CLOSED is a simulator exit (target, stop, or time).
// OPEN means the entry filled and the bars ended before a strategy exit. UNFILLED means the entry
// session never traded the limit. NO_BARS means there was nothing to play.
const (
	RoundTripClosed   = "CLOSED"
	RoundTripOpen     = "OPEN"
	RoundTripUnfilled = "UNFILLED"
	RoundTripNoBars   = "NO_BARS"
)

// RoundTrip is what the hourly simulator does with one stored signal. Trade.ReturnPct is a
// fraction (0.05 is five percent). On OPEN, Trade carries the entry and no strategy exit. On
// UNFILLED, Trade is empty.
type RoundTrip struct {
	Outcome string
	Trade   models.Trade
}

// ReplayRoundTrip plays one signal through the same entry model and exit rules as Run, sized to
// a single share so the return does not depend on account equity. daily are the symbol's daily
// bars and hourly its hourly bars (nil is fine: each session is then played as its daily bar, one
// bar, so nothing exits on the bar that entered it). Dates may carry a time; they are compared on
// the first ten characters.
func ReplayRoundTrip(signal models.Signal, daily, hourly []models.Bar, cfg strategy.StrategyConfig) RoundTrip {
	if signal.Symbol == "" {
		return RoundTrip{Outcome: RoundTripNoBars}
	}
	bars, _ := Bars(daily, hourly)
	if len(bars) == 0 {
		return RoundTrip{Outcome: RoundTripNoBars}
	}
	seen := map[string]bool{}
	var dates []string
	for _, b := range bars {
		if d := b.Session(); !seen[d] {
			seen[d] = true
			dates = append(dates, d)
		}
	}
	sort.Strings(dates)

	cfg.PositionSizing = ""
	c := Config(cfg, 1_000_000)
	c.FixedShares = 1
	c.PositionCap = max(cfg.PositionCap, 1)
	res := sim.Run(Signals([]models.Signal{signal}), map[string][]sim.Bar{signal.Symbol: bars}, dates, c)

	if len(res.Trades) == 0 {
		if len(res.Open) == 0 {
			return RoundTrip{Outcome: RoundTripUnfilled}
		}
		o := res.Open[0]
		return RoundTrip{Outcome: RoundTripOpen, Trade: models.Trade{Symbol: o.Symbol, EntryDate: day(o.Entry), EntryPrice: o.EntryPrice}}
	}
	tr := Trade(1, res.Trades[0], cfg.CommissionPerShare)
	if tr.ExitReason == models.ExitReasonEndBacktest {
		return RoundTrip{Outcome: RoundTripOpen, Trade: models.Trade{Symbol: tr.Symbol, EntryDate: tr.EntryDate, EntryPrice: tr.EntryPrice}}
	}
	return RoundTrip{Outcome: RoundTripClosed, Trade: tr}
}

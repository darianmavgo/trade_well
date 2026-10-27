package hoursim

import (
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sim"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// Input is one strategy run: the signals its pipeline wrote, the bars to play them over and the
// execution settings.
type Input struct {
	Signals []models.Signal
	// Daily is every symbol's daily session bars; Hourly its hourly bars (Eastern
	// "YYYY-MM-DD HH:MM", regular hours, in time order), which may be nil or hold only some symbols.
	Daily  map[string][]models.Bar
	Hourly map[string][]models.Bar
	// Dates is the sorted calendar of sessions to simulate.
	Dates     []string
	Config    strategy.StrategyConfig
	Capital   float64
	Benchmark map[string]models.Bar
	// Dividends, when set, cannot be played: the hourly simulator pays none.
	Dividends map[string]map[string]float64
}

// Output is the finished run in the types reports and storage use.
type Output struct {
	Report models.PerformanceReport
	Trades []models.Trade
	Curve  []models.DailyEquityPoint
	// DailySessions maps a symbol to the sessions played as a single daily bar because it had no
	// hourly bars for them. A trade on such a session was not judged hour by hour.
	DailySessions map[string][]string
}

// Run plays one strategy's signals over hourly bars.
func Run(in Input) (Output, error) {
	if err := Supported(in.Config); err != nil {
		return Output{}, err
	}
	if len(in.Dividends) > 0 {
		return Output{}, UnsupportedError{"dividends"}
	}
	bars, daily := barsFor(in, in.Signals)
	res := sim.Run(Signals(in.Signals), bars, in.Dates, Config(in.Config, in.Capital))
	return finish(in, res, daily), nil
}

// barsFor builds sim bars for the symbols the signals name.
func barsFor(in Input, signals []models.Signal) (map[string][]sim.Bar, map[string][]string) {
	out := map[string][]sim.Bar{}
	daily := map[string][]string{}
	for _, s := range signals {
		if _, done := out[s.Symbol]; done {
			continue
		}
		b, ds := Bars(in.Daily[s.Symbol], in.Hourly[s.Symbol])
		out[s.Symbol] = b
		if len(ds) > 0 {
			daily[s.Symbol] = ds
		}
	}
	return out, daily
}

func finish(in Input, res *sim.Result, daily map[string][]string) Output {
	capital := res.Initial
	trades := make([]models.Trade, len(res.Trades))
	for i, t := range res.Trades {
		trades[i] = Trade(i+1, t, in.Config.CommissionPerShare)
	}
	curve := Curve(res.Curve, capital)
	return Output{
		Report:        analytics.CalculatePerformanceMetricsWithBenchmark(capital, trades, curve, in.Benchmark),
		Trades:        trades,
		Curve:         curve,
		DailySessions: daily,
	}
}

// Curve maps sim's session-close points to the equity curve reports use, adding the daily
// return and the drawdown from the running peak.
func Curve(points []sim.Point, capital float64) []models.DailyEquityPoint {
	out := make([]models.DailyEquityPoint, 0, len(points))
	peak := capital
	prev := 0.0
	for i, p := range points {
		if p.Equity > peak {
			peak = p.Equity
		}
		pt := models.DailyEquityPoint{
			Date: p.Date, Cash: p.Cash, TotalEquity: p.Equity, PositionsValue: p.Equity - p.Cash,
			OpenPositions: p.OpenPositions, BuyingPower: p.Cash,
		}
		if peak > 0 {
			pt.DrawdownPct = (peak - p.Equity) / peak
		}
		if i > 0 && prev > 0 {
			pt.DailyReturn = (p.Equity - prev) / prev
		}
		prev = p.Equity
		out = append(out, pt)
	}
	return out
}

// SortedSymbols lists the symbols of a DailySessions map in order, for stable messages.
func SortedSymbols(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

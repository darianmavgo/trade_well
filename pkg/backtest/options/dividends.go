package options

import "github.com/darianmavgo/trade_well/pkg/backtest/models"

// DividendsFromAdjClose recovers cash dividends per share (ex-date → $) from
// steps in AdjClose/Close. The stored bars carry no dividend column, but Yahoo's
// adjustment factor f = AdjClose/Close rises at each ex-date by
// 1/(1 - D/prevClose), so D = prevClose * (1 - f_prev/f_ex). Bars must be
// oldest-first; steps under 0.02% are treated as rounding noise.
func DividendsFromAdjClose(bars []models.Bar) map[string]float64 {
	out := map[string]float64{}
	for i := 1; i < len(bars); i++ {
		p, c := bars[i-1], bars[i]
		if p.Close <= 0 || c.Close <= 0 || p.AdjClose <= 0 || c.AdjClose <= 0 {
			continue
		}
		fp, fc := p.AdjClose/p.Close, c.AdjClose/c.Close
		if fc/fp > 1+2e-4 {
			out[c.Date] = p.Close * (1 - fp/fc)
		}
	}
	return out
}

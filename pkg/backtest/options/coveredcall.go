package options

import (
	"math"
	"sort"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// CoveredCallConfig describes a monthly buy-and-hold + short-call overlay.
type CoveredCallConfig struct {
	Underlying   string
	Capital      float64
	OTMPct       float64 // target strike = spot * (1 + OTMPct/100) at each roll
	Start        string  // earliest roll date (YYYY-MM-DD); "" = as early as option data allows
	Commission   float64 // $ per contract when the call is sold (expiry/assignment are free)
	SlipPerShare float64 // $ per share given up on the sale vs the last-trade price
	MaxQuoteAge  int     // calendar days a last-trade price may be stale at the sell date
	// MaxStrikeMissPct bounds how far (in % of spot) the sold strike may sit from the
	// OTMPct target. Without it a "10% OTM" run would quietly sell a 5% strike whenever
	// the 10% one never traded, and results would depend on which strikes were downloaded.
	// 0 means 1.5.
	MaxStrikeMissPct float64

	// Stock-leg costs, applied to the initial buy and to every re-buy after a call is exercised.
	StockSlipPct    float64 // fraction of price paid above the close when buying
	StockCommission float64 // $ per share bought

	// Dividends (ex-date → $/share), if set, are paid on held shares and
	// withdrawn from the account at once: never reinvested, not in Cash.
	// TotalEquity in the curves then includes the running total withdrawn, so
	// the overlay and buy & hold are compared on total wealth.
	Dividends map[string]float64
}

// CoveredCallResult holds the simulated overlay and the same-shares buy & hold.
type CoveredCallResult struct {
	Trades        []models.Trade // one per sold call
	Equity        []models.DailyEquityPoint
	BuyHoldEquity []models.DailyEquityPoint
	Cycles        int     // monthly windows covered
	Skipped       int     // windows with no usable quote at the roll date (held naked long)
	Assigned      int     // expired in the money: shares called away at the strike
	Rebuys        int     // times cash was redeployed into shares after being called away
	Premium       float64 // total net premium collected
	Settlement    float64 // intrinsic value given up at expiry (upside surrendered)

	WithdrawnDividends        float64 // overlay: dividends paid out and withdrawn
	BuyHoldWithdrawnDividends float64 // buy & hold benchmark's withdrawn dividends
	FinalAccountValue         float64 // cash + shares at the last bar, excluding withdrawn dividends
}

type callSeries = storage.OptionChainSeries

// lastBarOnOrBefore returns the newest bar with Date <= date.
func lastBarOnOrBefore(bars []models.OptionBar, date string) (models.OptionBar, bool) {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Date > date })
	if i == 0 {
		return models.OptionBar{}, false
	}
	return bars[i-1], true
}

func daysBetween(a, b string) int {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}

// SimulateCoveredCall holds the underlying and sells one call per 100 shares
// each month, rolling on the previous monthly expiry. A call that expires in
// the money is exercised: shares are called away at the strike and the cash is
// immediately redeployed into shares at that day's close (whole shares; the
// remainder stays as cash).
//
// The option leg is priced from stored last-trade EOD bars, so it inherits
// their limits: thin strikes lack bars on some days (the last trade is carried
// forward, floored at intrinsic value) and the sale price is a last trade less
// SlipPerShare, not a bid.
func SimulateCoveredCall(cfg CoveredCallConfig, bars []models.Bar, chains map[string][]callSeries) CoveredCallResult {
	var res CoveredCallResult
	if len(bars) == 0 || len(chains) == 0 || cfg.Capital <= 0 {
		return res
	}
	if cfg.MaxQuoteAge <= 0 {
		cfg.MaxQuoteAge = 5
	}
	if cfg.MaxStrikeMissPct <= 0 {
		cfg.MaxStrikeMissPct = 1.5
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
	closeOn := make(map[string]float64, len(bars))
	dates := make([]string, len(bars))
	for i, b := range bars {
		d := b.Date[:10]
		dates[i] = d
		closeOn[d] = b.Close
	}
	lastDate := dates[len(dates)-1]
	lastOnOrBefore := func(d string) (string, bool) {
		i := sort.SearchStrings(dates, d)
		if i < len(dates) && dates[i] == d {
			return d, true
		}
		if i == 0 {
			return "", false
		}
		return dates[i-1], true
	}

	expiries := make([]string, 0, len(chains))
	for e := range chains {
		expiries = append(expiries, e)
	}
	sort.Strings(expiries)

	type cycle struct {
		sell, enter, expiry string // enter >= sell: first day the chosen strike actually traded
		series              *callSeries
		premium             float64
	}
	var cycles []cycle
	for _, e := range expiries {
		et, err := time.Parse("2006-01-02", e)
		if err != nil {
			continue
		}
		// Nominal roll date: prior third Friday; the Friday-holiday Thursday expiry keeps its own month.
		sell, ok := lastOnOrBefore(PrevMonthly(ThirdFriday(et.Year(), et.Month())).Format("2006-01-02"))
		if !ok || sell < cfg.Start || sell >= lastDate {
			continue
		}
		spot := closeOn[sell]
		// A strike is sellable if it printed a trade within MaxQuoteAge days of the roll date:
		// the latest trade on/before it, else the first one after it (delayed entry).
		type quote struct {
			bar   models.OptionBar
			enter string
		}
		quotes := map[float64]quote{}
		strikes := make([]float64, 0, len(chains[e]))
		for i := range chains[e] {
			bs := chains[e][i].Bars
			var q models.OptionBar
			var enter string
			if b, ok := lastBarOnOrBefore(bs, sell); ok && daysBetween(b.Date, sell) <= cfg.MaxQuoteAge {
				q, enter = b, sell
			} else if j := sort.Search(len(bs), func(j int) bool { return bs[j].Date > sell }); j < len(bs) && daysBetween(sell, bs[j].Date) <= cfg.MaxQuoteAge {
				q, enter = bs[j], bs[j].Date
			} else {
				continue
			}
			if cfg.OTMPct > 0 && chains[e][i].Contract.Strike <= spot {
				continue // never sell an in-the-money call when an OTM one is intended
			}
			quotes[chains[e][i].Contract.Strike] = quote{q, enter}
			strikes = append(strikes, chains[e][i].Contract.Strike)
		}
		c := cycle{sell: sell, enter: sell, expiry: e}
		if k, ok := NearestStrike(strikes, spot, cfg.OTMPct); ok && abs((k/spot-1)*100-cfg.OTMPct) <= cfg.MaxStrikeMissPct {
			for i := range chains[e] {
				if chains[e][i].Contract.Strike == k {
					c.series = &chains[e][i]
					c.enter = quotes[k].enter
					c.premium = math.Max(quotes[k].bar.Close-cfg.SlipPerShare, 0.01)
					break
				}
			}
		}
		cycles = append(cycles, c)
	}
	if len(cycles) == 0 {
		return res
	}

	startDate := cycles[0].sell
	if cfg.Start != "" { // honor the requested window even if the first months had no tradable call
		if i := sort.SearchStrings(dates, cfg.Start); i < len(dates) && dates[i] < startDate {
			startDate = dates[i]
		}
	}
	spot0 := closeOn[startDate]
	buyPx := func(px float64) float64 { return px*(1+cfg.StockSlipPct) + cfg.StockCommission }
	shares := int(cfg.Capital / buyPx(spot0))
	cash := cfg.Capital - float64(shares)*buyPx(spot0)
	bhShares, bhCash := shares, cash
	var cumDiv, bhCumDiv float64
	contracts := shares / 100

	var peak, bhPeak, prevEq, prevBH float64
	ci := 0 // index of the active cycle
	var open *cycle
	var openN int
	var openStrike float64
	var trade models.Trade

	emit := func(d string, liab float64) {
		px := closeOn[d]
		eq := cash + float64(shares)*px - liab + cumDiv
		bh := bhCash + float64(bhShares)*px + bhCumDiv
		p := models.DailyEquityPoint{Date: d, Cash: cash, PositionsValue: float64(shares) * px, TotalEquity: eq}
		q := models.DailyEquityPoint{Date: d, Cash: bhCash, PositionsValue: float64(bhShares) * px, TotalEquity: bh}
		if prevEq > 0 {
			p.DailyReturn = eq/prevEq - 1
			q.DailyReturn = bh/prevBH - 1
		}
		peak, bhPeak = math.Max(peak, eq), math.Max(bhPeak, bh)
		p.DrawdownPct, q.DrawdownPct = (eq-peak)/peak, (bh-bhPeak)/bhPeak
		if open != nil {
			p.OpenPositions = openN
		}
		res.Equity = append(res.Equity, p)
		res.BuyHoldEquity = append(res.BuyHoldEquity, q)
		prevEq, prevBH = eq, bh
	}

	for _, d := range dates {
		if d < startDate {
			continue
		}
		px := closeOn[d]

		// 0. Dividends on shares held into the ex-date are withdrawn immediately.
		if div := cfg.Dividends[d]; div > 0 {
			cumDiv += float64(shares) * div
			bhCumDiv += float64(bhShares) * div
		}

		// 1. Settle a call that expires today (before rolling, so the new call can be sold the same day).
		if open != nil && d >= open.expiry {
			intrinsic := math.Max(px-openStrike, 0)
			trade.ExitDate, trade.ExitPrice = d, intrinsic
			trade.ExitReason = models.ExitReasonTimeUp
			if intrinsic > 0 {
				// Exercised: shares go at the strike, then all cash buys back in at today's close.
				res.Assigned++
				called := openN * 100
				res.Settlement += intrinsic * float64(called) // upside surrendered vs holding
				cash += openStrike * float64(called)
				shares -= called
				if n := int(cash / buyPx(px)); n > 0 {
					shares += n
					cash -= float64(n) * buyPx(px)
					res.Rebuys++
				}
			}
			trade.GrossPnL = (trade.EntryPrice - intrinsic) * 100 * float64(openN)
			trade.NetPnL = trade.GrossPnL - trade.CommissionPaid
			if trade.InvestedCapital > 0 {
				trade.ReturnPct = trade.NetPnL / trade.InvestedCapital
			}
			trade.HoldDays = daysBetween(trade.EntryDate, d)
			res.Trades = append(res.Trades, trade)
			open = nil
		}

		// 2. Sell the next call once its entry day arrives.
		for open == nil && ci < len(cycles) && cycles[ci].enter <= d {
			c := &cycles[ci]
			ci++
			if c.expiry <= d {
				continue // window already over (e.g. gaps in the underlying bars)
			}
			res.Cycles++
			if c.series == nil || contracts == 0 {
				res.Skipped++
				continue
			}
			openN, openStrike = contracts, c.series.Contract.Strike
			credit := c.premium*100*float64(openN) - cfg.Commission*float64(openN)
			cash += credit
			res.Premium += credit
			open = c
			trade = models.Trade{
				ID: len(res.Trades) + 1, StrategyID: "voo-covered-call", Symbol: c.series.Contract.Ticker,
				OrderType: "sell_to_open", EntryDate: d, EntryPrice: c.premium, Shares: openN * 100,
				InvestedCapital: px * 100 * float64(openN), CommissionPaid: cfg.Commission * float64(openN),
				TargetPrice: openStrike,
			}
		}

		// 3. Mark the short call: last trade carried forward, never below intrinsic.
		liab := 0.0
		if open != nil {
			mark := math.Max(px-openStrike, 0)
			if q, ok := lastBarOnOrBefore(open.series.Bars, d); ok && q.Close > mark {
				mark = q.Close
			}
			liab = mark * 100 * float64(openN)
		}
		emit(d, liab)
	}

	res.WithdrawnDividends, res.BuyHoldWithdrawnDividends = cumDiv, bhCumDiv
	if n := len(res.Equity); n > 0 {
		res.FinalAccountValue = res.Equity[n-1].TotalEquity - cumDiv
	}
	if open != nil { // window ended with the call still open: book it as of the last bar
		intrinsic := math.Max(closeOn[lastDate]-openStrike, 0)
		trade.ExitDate, trade.ExitPrice, trade.ExitReason = lastDate, intrinsic, models.ExitReasonEndBacktest
		trade.GrossPnL = (trade.EntryPrice - intrinsic) * 100 * float64(openN)
		trade.NetPnL = trade.GrossPnL - trade.CommissionPaid
		if trade.InvestedCapital > 0 {
			trade.ReturnPct = trade.NetPnL / trade.InvestedCapital
		}
		trade.HoldDays = daysBetween(trade.EntryDate, lastDate)
		res.Trades = append(res.Trades, trade)
	}
	return res
}

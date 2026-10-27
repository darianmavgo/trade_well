package hoursim

import (
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sim"
)

// dailyStamp is the clock time a session with no hourly bars is played at.
const dailyStamp = " 09:00"

// Bars builds one symbol's bars for the hourly simulator from its daily bars and its hourly
// bars (Eastern "YYYY-MM-DD HH:MM", regular session hours, in time order; nil if none).
//
// A session with hourly bars is played on them. The hour that starts at 9:00 holds the half hour
// before the open, so its open is replaced by the daily open and its range is cut to the daily
// range, which is regular session only. A session inside the span the hourly bars cover but with
// none of its own has no bar: nothing can confirm a fill there. A session outside that span, or
// of a symbol with no hourly bars at all, is played as its daily bar, one bar for the session.
// dailySessions lists the sessions that used the daily bar.
func Bars(daily, hourly []models.Bar) (bars []sim.Bar, dailySessions []string) {
	byDay := map[string][]models.Bar{}
	var first, last string
	for _, h := range hourly {
		d := day(h.Date)
		byDay[d] = append(byDay[d], h)
	}
	if len(hourly) > 0 {
		first, last = day(hourly[0].Date), day(hourly[len(hourly)-1].Date)
	}
	dailyBy := map[string]models.Bar{}
	days := map[string]bool{}
	for _, b := range daily {
		d := day(b.Date)
		dailyBy[d] = b
		days[d] = true
	}
	for d := range byDay {
		days[d] = true
	}
	sorted := make([]string, 0, len(days))
	for d := range days {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)

	for _, d := range sorted {
		db, hasDaily := dailyBy[d]
		if hs, ok := byDay[d]; ok {
			for _, h := range hs {
				if hasDaily && len(h.Date) >= 13 && h.Date[11:13] == "09" {
					h.Open = db.Open
					h.Low = max(h.Low, db.Low)
					h.High = min(h.High, db.High)
				}
				bars = append(bars, sim.Bar{T: h.Date, Open: h.Open, High: h.High, Low: h.Low, Close: h.Close, Volume: float64(h.Volume)})
			}
			continue
		}
		if !hasDaily || (len(hourly) > 0 && d >= first && d <= last) {
			continue
		}
		bars = append(bars, sim.Bar{T: d + dailyStamp, Open: db.Open, High: db.High, Low: db.Low, Close: db.Close, Volume: float64(db.Volume)})
		dailySessions = append(dailySessions, d)
	}
	return bars, dailySessions
}

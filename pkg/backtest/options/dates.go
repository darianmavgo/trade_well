// Package options plans option-history downloads and simulates option
// overlays (covered calls) on top of stored underlying bars.
package options

import "time"

// ThirdFriday returns the standard monthly option expiration date.
func ThirdFriday(year int, month time.Month) time.Time {
	d := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Friday {
		d = d.AddDate(0, 0, 1)
	}
	return d.AddDate(0, 0, 14)
}

// MonthlyExpiries lists the nominal monthly expirations in [from, to].
// Holiday-shifted expiries (Good Friday → Thursday) are resolved by the caller.
func MonthlyExpiries(from, to time.Time) []time.Time {
	var out []time.Time
	m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !m.After(to) {
		e := ThirdFriday(m.Year(), m.Month())
		if !e.Before(from) && !e.After(to) {
			out = append(out, e)
		}
		m = m.AddDate(0, 1, 0)
	}
	return out
}

// PrevMonthly is the nominal monthly expiry one month before e.
func PrevMonthly(e time.Time) time.Time {
	p := time.Date(e.Year(), e.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return ThirdFriday(p.Year(), p.Month())
}

// NearestStrike picks the listed strike closest to ref*(1+otmPct/100).
func NearestStrike(strikes []float64, ref, otmPct float64) (float64, bool) {
	if len(strikes) == 0 {
		return 0, false
	}
	target := ref * (1 + otmPct/100)
	best, bestDiff := strikes[0], abs(strikes[0]-target)
	for _, s := range strikes[1:] {
		if d := abs(s - target); d < bestDiff {
			best, bestDiff = s, d
		}
	}
	return best, true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

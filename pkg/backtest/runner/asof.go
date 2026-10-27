package runner

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// LastCompletedEquitySession is the most recent session date at or before now
// in America/New_York. Before the regular close (16:00 ET), the prior day is
// treated as last completed. Weekends and exchange holidays walk backward.
func LastCompletedEquitySession(now time.Time) time.Time {
	return calendar.LastCompletedSession(now)
}

// NextEquitySession returns the next session after last completed (the
// session live entries are aimed at after the tip close), skipping weekends and
// exchange holidays.
func NextEquitySession(lastCompleted time.Time) time.Time {
	d := lastCompleted.UTC().AddDate(0, 0, 1)
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	for !calendar.IsSession(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// ResolveLiveAsOf picks the bar date livescan should evaluate. Tip must equal
// last completed session — never run on stale. Signals on that tip are
// decisions for the next equity session.
func ResolveLiveAsOf(tipYYYYMMDD string, now time.Time) (asOf string, nextSession string, err error) {
	tip := strings.TrimSpace(tipYYYYMMDD)
	if len(tip) >= 10 {
		tip = tip[:10]
	}
	if tip == "" {
		return "", "", fmt.Errorf("empty market DB tip date")
	}
	expected := LastCompletedEquitySession(now).Format("2006-01-02")
	next := NextEquitySession(LastCompletedEquitySession(now)).Format("2006-01-02")
	if tip < expected {
		return "", "", fmt.Errorf("STALE_MARKET_DATA: market DB tip as-of %s but last completed session is %s (entries would target %s). Refresh failed to bring history current — fix the download source and retry", tip, expected, next)
	}
	return tip, next, nil
}

// DropUnfinishedSessions removes the bars dated after the last completed session as of now.
// A daily row for a session that is still open is provisional: one derived from the hours
// traded so far (sessions.Derive) looks like a finished day and would make the scan decide on
// half a session. It returns the bars and the sorted dates that remain.
func DropUnfinishedSessions(bySymbol map[string][]models.Bar, now time.Time) (map[string][]models.Bar, []string) {
	last := LastCompletedEquitySession(now).Format("2006-01-02")
	kept := make(map[string][]models.Bar, len(bySymbol))
	seen := map[string]bool{}
	var dates []string
	for sym, bars := range bySymbol {
		for _, b := range bars {
			if b.Date > last {
				continue
			}
			kept[sym] = append(kept[sym], b)
			if !seen[b.Date] {
				seen[b.Date] = true
				dates = append(dates, b.Date)
			}
		}
	}
	sort.Strings(dates)
	return kept, dates
}

package strateval

import (
	"fmt"
	"time"
)

// Split holds the in-sample / out-of-sample date bounds derived from a
// chronological trading-day list. OOS is the last OOSMonths calendar months;
// IS is everything strictly before OOSStart.
type Split struct {
	ISStart  string
	ISEnd    string // last IS trading day (inclusive)
	OOSStart string // first OOS trading day (inclusive)
	OOSEnd   string
}

// SplitDates partitions sortedDates (ascending YYYY-MM-DD) into IS and OOS.
// oosMonths must be >= 1. Returns an error if either side would be empty.
func SplitDates(sortedDates []string, oosMonths int) (Split, error) {
	if oosMonths < 1 {
		return Split{}, fmt.Errorf("oosMonths must be >= 1, got %d", oosMonths)
	}
	if len(sortedDates) < 2 {
		return Split{}, fmt.Errorf("need at least 2 trading days to split, got %d", len(sortedDates))
	}
	last := sortedDates[len(sortedDates)-1]
	t, err := time.Parse("2006-01-02", last)
	if err != nil {
		return Split{}, fmt.Errorf("parse last date %q: %w", last, err)
	}
	oosStartDay := t.AddDate(0, -oosMonths, 0).Format("2006-01-02")

	var is, oos []string
	for _, d := range sortedDates {
		if d >= oosStartDay {
			oos = append(oos, d)
		} else {
			is = append(is, d)
		}
	}
	if len(is) == 0 {
		return Split{}, fmt.Errorf("in-sample empty with oosMonths=%d (oosStart=%s)", oosMonths, oosStartDay)
	}
	if len(oos) == 0 {
		return Split{}, fmt.Errorf("out-of-sample empty with oosMonths=%d (oosStart=%s)", oosMonths, oosStartDay)
	}
	return Split{
		ISStart:  is[0],
		ISEnd:    is[len(is)-1],
		OOSStart: oos[0],
		OOSEnd:   oos[len(oos)-1],
	}, nil
}

// FilterDatesInclusive returns dates in [start, end] (both inclusive).
// Empty start/end means unbounded on that side.
func FilterDatesInclusive(sortedDates []string, start, end string) []string {
	out := make([]string, 0, len(sortedDates))
	for _, d := range sortedDates {
		if start != "" && d < start {
			continue
		}
		if end != "" && d > end {
			continue
		}
		out = append(out, d)
	}
	return out
}

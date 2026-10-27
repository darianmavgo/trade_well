package walk_forward

import (
	"fmt"
	"sort"
	"time"
)

// Fold is one rolling train/test step. IS is the trainMonths immediately
// before OOSStart. OOS is the following testMonths. The next fold starts
// stepMonths later. Dates are inclusive trading days (YYYY-MM-DD).
type Fold struct {
	Index    int
	ISStart  string
	ISEnd    string
	OOSStart string
	OOSEnd   string
}

// Folds partitions sorted trading days into rolling walk-forward windows.
// trainMonths, testMonths and stepMonths are calendar months and must be >= 1.
func Folds(sortedDates []string, trainMonths, testMonths, stepMonths int) ([]Fold, error) {
	if trainMonths < 1 || testMonths < 1 || stepMonths < 1 {
		return nil, fmt.Errorf("walk_forward: train, test and step months must be >= 1")
	}
	dates := normalizeDates(sortedDates)
	sort.Strings(dates)
	dates = normalizeDates(dates) // drop duplicates created by the sort of equal days
	if len(dates) < 2 {
		return nil, fmt.Errorf("walk_forward: need at least 2 trading days, got %d", len(dates))
	}
	first, err := time.Parse("2006-01-02", dates[0])
	if err != nil {
		return nil, fmt.Errorf("walk_forward: parse %q: %w", dates[0], err)
	}
	last := dates[len(dates)-1]
	var out []Fold
	cursor := first.AddDate(0, trainMonths, 0)
	for n := 0; ; n++ {
		oosStart := firstOnOrAfter(dates, cursor.Format("2006-01-02"))
		oosEnd := lastBefore(dates, cursor.AddDate(0, testMonths, 0).Format("2006-01-02"))
		isStart := firstOnOrAfter(dates, cursor.AddDate(0, -trainMonths, 0).Format("2006-01-02"))
		isEnd := lastBefore(dates, cursor.Format("2006-01-02"))
		if oosStart == "" || oosEnd == "" || isStart == "" || isEnd == "" || oosEnd < oosStart || isEnd < isStart {
			break
		}
		if oosStart > last {
			break
		}
		out = append(out, Fold{Index: n, ISStart: isStart, ISEnd: isEnd, OOSStart: oosStart, OOSEnd: oosEnd})
		cursor = cursor.AddDate(0, stepMonths, 0)
		if cursor.Format("2006-01-02") > last {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("walk_forward: no fold fits %s..%s with train=%d test=%d", dates[0], last, trainMonths, testMonths)
	}
	return out, nil
}

func normalizeDates(in []string) []string {
	out := make([]string, 0, len(in))
	var prev string
	for _, d := range in {
		if len(d) >= 10 {
			d = d[:10]
		}
		if d == "" || d == prev {
			continue
		}
		out = append(out, d)
		prev = d
	}
	return out
}

func firstOnOrAfter(dates []string, day string) string {
	for _, d := range dates {
		if d >= day {
			return d
		}
	}
	return ""
}

func lastBefore(dates []string, day string) string {
	var last string
	for _, d := range dates {
		if d >= day {
			break
		}
		last = d
	}
	return last
}

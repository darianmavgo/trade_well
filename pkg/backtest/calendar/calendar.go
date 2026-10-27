// Package calendar answers US equity-market (NYSE) trading-day questions:
// which dates are full-day holidays and how many sessions lie between two
// dates. Early closes (day after Thanksgiving, Christmas Eve, Jul 3) are
// still sessions. Rules encoded here are the ones in force since 2022
// (Juneteenth is a holiday from 2022 on).
package calendar

import "time"

// IsHoliday reports whether the NYSE is closed all day on t's calendar date
// (weekends are not holidays here; see IsSession).
func IsHoliday(t time.Time) bool {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	// A holiday observed on a Friday can fall in the previous calendar year
	// only for Jan 1 (NYSE does not observe that one), so checking the
	// target year, plus the next year's Jan 1 rule below, is enough.
	for _, h := range holidays(d.Year()) {
		if h.Equal(d) {
			return true
		}
	}
	return false
}

// IsSession reports whether t's date is a regular trading day: a weekday
// that is not a holiday.
func IsSession(t time.Time) bool {
	wd := t.Weekday()
	return wd != time.Saturday && wd != time.Sunday && !IsHoliday(t)
}

// TradingDaysBetween counts sessions in (start, end]: the days after start's
// date up to and including end's date. It is 0 when end is not after start.
// This is the "days held" convention: a position entered on a Friday and
// checked the next Monday has been held 1 trading day.
func TradingDaysBetween(start, end time.Time) int {
	cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	target := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	days := 0
	for cur.Before(target) {
		cur = cur.AddDate(0, 0, 1)
		if IsSession(cur) {
			days++
		}
	}
	return days
}

// AddTradingDays returns the date n sessions after start's date, the inverse of
// TradingDaysBetween: TradingDaysBetween(start, AddTradingDays(start, n)) == n.
// n <= 0 returns start's date. The result is midnight UTC of that date.
func AddTradingDays(start time.Time, n int) time.Time {
	cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	for n > 0 {
		cur = cur.AddDate(0, 0, 1)
		if IsSession(cur) {
			n--
		}
	}
	return cur
}

// holidays returns the observed NYSE full-day closures for year.
func holidays(year int) []time.Time {
	d := func(m time.Month, day int) time.Time { return time.Date(year, m, day, 0, 0, 0, 0, time.UTC) }
	var out []time.Time

	// New Year's Day: Sunday -> Monday; Saturday is NOT observed on Friday.
	if ny := d(time.January, 1); ny.Weekday() != time.Saturday {
		out = append(out, observed(ny, false))
	}
	out = append(out,
		nthWeekday(year, time.January, time.Monday, 3),    // Martin Luther King Jr. Day
		nthWeekday(year, time.February, time.Monday, 3),   // Washington's Birthday
		easter(year).AddDate(0, 0, -2),                    // Good Friday
		lastWeekday(year, time.May, time.Monday),          // Memorial Day
		nthWeekday(year, time.September, time.Monday, 1),  // Labor Day
		nthWeekday(year, time.November, time.Thursday, 4), // Thanksgiving
		observed(d(time.December, 25), true),              // Christmas
		observed(d(time.July, 4), true),                   // Independence Day
	)
	if year >= 2022 {
		out = append(out, observed(d(time.June, 19), true)) // Juneteenth
	}
	return out
}

// observed shifts a fixed-date holiday: Sunday -> Monday, and (when
// satToFri) Saturday -> Friday.
func observed(t time.Time, satToFri bool) time.Time {
	switch t.Weekday() {
	case time.Sunday:
		return t.AddDate(0, 0, 1)
	case time.Saturday:
		if satToFri {
			return t.AddDate(0, 0, -1)
		}
	}
	return t
}

func nthWeekday(year int, m time.Month, wd time.Weekday, n int) time.Time {
	first := time.Date(year, m, 1, 0, 0, 0, 0, time.UTC)
	off := (int(wd) - int(first.Weekday()) + 7) % 7
	return first.AddDate(0, 0, off+7*(n-1))
}

func lastWeekday(year int, m time.Month, wd time.Weekday) time.Time {
	last := time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC)
	off := (int(last.Weekday()) - int(wd) + 7) % 7
	return last.AddDate(0, 0, -off)
}

// easter returns Easter Sunday (Gregorian, anonymous algorithm).
func easter(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

package calendar

import "time"

// LastCompletedSession is the most recent session date at or before now
// in America/New_York. Before the regular close (16:00 ET), the prior day is
// treated as last completed. Weekends and exchange holidays walk backward.
func LastCompletedSession(now time.Time) time.Time {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("ET", -5*3600)
	}
	et := now.In(loc)
	if et.Hour() < 16 {
		et = et.AddDate(0, 0, -1)
	}
	day := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
	for !IsSession(day) {
		day = day.AddDate(0, 0, -1)
	}
	return day
}

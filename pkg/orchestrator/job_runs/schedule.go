package job_runs

import (
	"fmt"
	"time"
	_ "time/tzdata" // the zone database ships in the binary, so the lookup below cannot depend on the host
)

var easternTime = loadEastern()

func loadEastern() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// A fixed EST offset would be an hour wrong from March to November, shifting every
		// market-hours check, so stop instead of guessing.
		panic(fmt.Sprintf("job_runs: America/New_York is unavailable even from the embedded zone database: %v", err))
	}
	return loc
}

// EasternTime is the market-clock location (America/New_York). Every schedule below is in it.
func EasternTime() *time.Location { return easternTime }

var (
	monFri   = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	sunThu   = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday}
	allDays  = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	saturday = []time.Weekday{time.Saturday}
)

// NotRecordedEachRun lists cron jobs that are deliberately not in Schedule: they run every few
// minutes and record a job_run row only when they acted, so a quiet run is not a missed slot.
var NotRecordedEachRun = []string{"trigger_limits"}

// JobSpec is when a scheduled job runs. check_health compares it with the
// job_run history to say which job did not do its job. Times are "15:04" in EasternTime.
type JobSpec struct {
	Name  string
	Days  []time.Weekday
	Times []string
	// NotBefore ("2006-01-02", ET) is the first date a slot counts. A job added to the schedule
	// is not reported as never having run for slots that came before it was deployed.
	NotBefore string
}

// Schedule lists the scheduled jobs in pipeline order: a job's outputs feed the
// ones after it, so the first broken entry is the likely root cause. It must
// match deploy_gae/cron.yaml; a test parses that file and fails on any
// difference.
var Schedule = []JobSpec{
	{Name: "account_state", Days: monFri, Times: []string{"09:15", "16:15"}},
	// risk reads the account_state sync before it: it sets what the 18:15 staging and the next
	// morning's submissions may do.
	{Name: "risk", Days: monFri, Times: []string{"09:17", "16:18"}, NotBefore: "2026-10-16"},
	{Name: "scan_market", Days: sunThu, Times: []string{"18:00"}},
	{Name: "stage_orders", Days: sunThu, Times: []string{"18:15"}},
	{Name: "submit_orders", Days: monFri, Times: []string{"09:28", "09:45", "16:20"}},
	// Cancels entries resting unfilled for over 24h before the morning submit, so they free buying power.
	{Name: "cancel_stale_entries", Days: monFri, Times: []string{"09:05"}, NotBefore: "2026-10-29"},
	{Name: "manage_exits", Days: monFri, Times: []string{"09:35", "15:50"}},
	{Name: "check_health", Days: monFri, Times: []string{"10:00"}},
	// Not pipeline stages: they report on it. Keep them last (downstreamOf leaves them out).
	{Name: "ready", Days: allDays, Times: []string{"09:00", "17:30"}, NotBefore: "2026-10-14"},
	{Name: "weekly_digest", Days: saturday, Times: []string{"09:00"}, NotBefore: "2026-10-14"},
}

// LastExpected returns the most recent scheduled run time of spec at or before
// now, and false if there is none within the last 8 days.
func LastExpected(spec JobSpec, now time.Time) (time.Time, bool) {
	loc := EasternTime()
	local := now.In(loc)
	for back := 0; back <= 8; back++ {
		day := time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, loc)
		if !hasDay(spec.Days, day.Weekday()) {
			continue
		}
		var best time.Time
		for _, hhmm := range spec.Times {
			t, err := time.ParseInLocation("15:04", hhmm, loc)
			if err != nil {
				continue
			}
			at := time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if !at.After(local) && at.After(best) {
				best = at
			}
		}
		if !best.IsZero() {
			if spec.NotBefore != "" && best.Format("2006-01-02") < spec.NotBefore {
				return time.Time{}, false
			}
			return best, true
		}
	}
	return time.Time{}, false
}

// NextExpected returns the first scheduled run time of spec after now, and false if there is
// none within the next 8 days.
func NextExpected(spec JobSpec, now time.Time) (time.Time, bool) {
	loc := EasternTime()
	local := now.In(loc)
	for ahead := 0; ahead <= 8; ahead++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+ahead, 0, 0, 0, 0, loc)
		if !hasDay(spec.Days, day.Weekday()) {
			continue
		}
		var best time.Time
		for _, hhmm := range spec.Times {
			t, err := time.ParseInLocation("15:04", hhmm, loc)
			if err != nil {
				continue
			}
			at := time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if at.After(local) && (best.IsZero() || at.Before(best)) {
				best = at
			}
		}
		if !best.IsZero() {
			return best, true
		}
	}
	return time.Time{}, false
}

func hasDay(days []time.Weekday, d time.Weekday) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

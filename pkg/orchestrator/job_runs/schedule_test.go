package job_runs

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func et(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, EasternTime())
}

func spec(name string) JobSpec {
	for _, s := range Schedule {
		if s.Name == name {
			return s
		}
	}
	panic("no spec " + name)
}

func TestLastExpected(t *testing.T) {
	cases := []struct {
		name string
		job  string
		now  time.Time
		want time.Time
	}{
		// Thu 2026-09-24
		{"same day, latest slot already passed", "submit_orders", et(2026, 9, 24, 12, 0), et(2026, 9, 24, 9, 45)},
		{"same day, between slots", "manage_exits", et(2026, 9, 24, 12, 0), et(2026, 9, 24, 9, 35)},
		{"before the first slot goes back a day", "account_state", et(2026, 9, 24, 8, 0), et(2026, 9, 23, 16, 15)},
		{"exactly on the slot counts", "check_health", et(2026, 9, 24, 10, 0), et(2026, 9, 24, 10, 0)},
		{"Sun-Thu job on Friday evening is still Thursday", "scan_market", et(2026, 9, 25, 19, 0), et(2026, 9, 24, 18, 0)},
		{"Sun-Thu job on Saturday", "stage_orders", et(2026, 9, 26, 12, 0), et(2026, 9, 24, 18, 15)},
		{"Mon-Fri job on Saturday", "submit_orders", et(2026, 9, 26, 12, 0), et(2026, 9, 25, 16, 20)},
		{"Mon-Fri job on Monday morning goes to Friday", "account_state", et(2026, 9, 28, 9, 0), et(2026, 9, 25, 16, 15)},
		{"Sun-Thu job on Sunday evening", "scan_market", et(2026, 9, 27, 18, 30), et(2026, 9, 27, 18, 0)},
	}
	for _, c := range cases {
		got, ok := LastExpected(spec(c.job), c.now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: %s at %v = %v (ok=%v), want %v", c.name, c.job, c.now, got, ok, c.want)
		}
	}
}

// TestScheduleMatchesCronYAML fails when Schedule and the real deploy file
// disagree, so the "did it run when it should" check can never drift from what
// App Engine actually schedules.
func TestScheduleMatchesCronYAML(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy_gae/cron.yaml")
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(`url:\s*/cron/(\w+)`)
	sch := regexp.MustCompile(`schedule:\s*every\s+([a-z,]+)\s+(\d\d:\d\d)`)
	names := map[string]time.Weekday{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

	got := map[string]bool{}
	var job string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := url.FindStringSubmatch(line); m != nil {
			job = m[1]
		}
		if slices.Contains(NotRecordedEachRun, job) {
			continue
		}
		if m := sch.FindStringSubmatch(line); m != nil {
			for _, d := range strings.Split(m[1], ",") {
				got[job+"|"+time.Weekday(names[d]).String()+"|"+m[2]] = true
			}
		}
	}

	want := map[string]bool{}
	for _, s := range Schedule {
		for _, d := range s.Days {
			for _, hhmm := range s.Times {
				want[s.Name+"|"+d.String()+"|"+hhmm] = true
			}
		}
	}
	diff := func(a, b map[string]bool) []string {
		var out []string
		for k := range a {
			if !b[k] {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	if d := diff(got, want); len(d) > 0 {
		t.Errorf("in cron.yaml but not in job_runs.Schedule: %v", d)
	}
	if d := diff(want, got); len(d) > 0 {
		t.Errorf("in job_runs.Schedule but not in cron.yaml: %v", d)
	}
}

func TestNextExpected(t *testing.T) {
	cases := []struct {
		name string
		job  string
		now  time.Time
		want time.Time
	}{
		// Thu 2026-09-24
		{"later today", "submit_orders", et(2026, 9, 24, 9, 30), et(2026, 9, 24, 9, 45)},
		{"after the last slot goes to tomorrow", "check_health", et(2026, 9, 24, 12, 0), et(2026, 9, 25, 10, 0)},
		{"Friday evening to Sunday for a Sun-Thu job", "scan_market", et(2026, 9, 25, 19, 0), et(2026, 9, 27, 18, 0)},
		{"exactly on the slot is the next one", "check_health", et(2026, 9, 24, 10, 0), et(2026, 9, 25, 10, 0)},
	}
	for _, c := range cases {
		got, ok := NextExpected(spec(c.job), c.now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: %s at %v = %v (ok=%v), want %v", c.name, c.job, c.now, got, ok, c.want)
		}
	}
}

func TestNotBeforeKeepsEarlierSlotsOutOfTheCheck(t *testing.T) {
	s := JobSpec{Name: "x", Days: saturday, Times: []string{"09:00"}, NotBefore: "2026-10-14"}
	if _, ok := LastExpected(s, et(2026, 10, 13, 12, 0)); ok {
		t.Error("the Saturday slot of 2026-10-10 came before NotBefore and must not count")
	}
	if got, ok := LastExpected(s, et(2026, 10, 17, 12, 0)); !ok || !got.Equal(et(2026, 10, 17, 9, 0)) {
		t.Errorf("Saturday 2026-10-17 09:00 should count, got %v ok=%v", got, ok)
	}
}

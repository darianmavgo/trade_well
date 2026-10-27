package calendar

import (
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestIsHoliday(t *testing.T) {
	yes := []time.Time{
		day(2025, 1, 1), day(2025, 1, 20), day(2025, 2, 17), day(2025, 4, 18), // NY, MLK, Presidents, Good Friday
		day(2025, 5, 26), day(2025, 6, 19), day(2025, 7, 4), day(2025, 9, 1),
		day(2025, 11, 27), day(2025, 12, 25),
		day(2026, 4, 3),   // Good Friday 2026
		day(2027, 12, 24), // Christmas Saturday -> observed Friday
		day(2026, 7, 3),   // Jul 4 Saturday -> observed Friday
		day(2022, 12, 26), // Christmas Sunday -> Monday
		day(2023, 1, 2),   // Jan 1 Sunday -> Monday
	}
	for _, d := range yes {
		if !IsHoliday(d) {
			t.Errorf("%s should be a holiday", d.Format("2006-01-02"))
		}
	}
	no := []time.Time{
		day(2022, 12, 30), // Jan 1 2022 was Saturday: NOT observed the Friday before
		day(2021, 6, 18),  // Juneteenth not yet a holiday
		day(2025, 11, 28), // early close, still a session
		day(2025, 12, 24),
	}
	for _, d := range no {
		if IsHoliday(d) {
			t.Errorf("%s should not be a holiday", d.Format("2006-01-02"))
		}
	}
}

func TestTradingDaysBetween(t *testing.T) {
	if got := TradingDaysBetween(day(2025, 1, 3), day(2025, 1, 6)); got != 1 { // Fri -> Mon
		t.Errorf("Fri->Mon = %d, want 1", got)
	}
	// Thu Nov 20 -> Mon Dec 1 2025: Fri 21, Mon 24, Tue 25, Wed 26, (Thu 27 closed), Fri 28, Mon Dec 1 = 6
	if got := TradingDaysBetween(day(2025, 11, 20), day(2025, 12, 1)); got != 6 {
		t.Errorf("across Thanksgiving = %d, want 6", got)
	}
	if got := TradingDaysBetween(day(2025, 4, 16), day(2025, 4, 21)); got != 2 { // Good Friday closed: Thu 17, Mon 21
		t.Errorf("across Good Friday = %d, want 2", got)
	}
	if got := TradingDaysBetween(day(2025, 1, 6), day(2025, 1, 3)); got != 0 {
		t.Errorf("reversed = %d, want 0", got)
	}
}

func TestAddTradingDaysInvertsTradingDaysBetween(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name  string
		start time.Time
		n     int
		want  time.Time
	}{
		{"zero days is the same date", d(2026, 9, 24), 0, d(2026, 9, 24)},
		{"Thursday + 1 = Friday", d(2026, 9, 24), 1, d(2026, 9, 25)},
		{"Friday + 1 skips the weekend", d(2026, 9, 25), 1, d(2026, 9, 28)},
		{"Friday + 5 is next Friday", d(2026, 9, 25), 5, d(2026, 10, 2)},
		{"skips a holiday: day before Labor Day + 1", d(2026, 9, 4), 1, d(2026, 9, 8)},
		{"a Saturday start: the next session is Monday", d(2026, 9, 26), 1, d(2026, 9, 28)},
	}
	for _, c := range cases {
		got := AddTradingDays(c.start, c.n)
		if !got.Equal(c.want) {
			t.Errorf("%s: AddTradingDays(%s, %d) = %s, want %s", c.name, c.start.Format("2006-01-02"), c.n, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
	// Round trip against the counting function, for every start day of a month.
	for day := 1; day <= 28; day++ {
		start := d(2026, 9, day)
		for n := 1; n <= 7; n++ {
			if got := TradingDaysBetween(start, AddTradingDays(start, n)); got != n {
				t.Errorf("round trip from %s with n=%d gave %d", start.Format("2006-01-02"), n, got)
			}
		}
	}
}

package schwab

import "testing"

func TestPeriodYearsRoundsUpToWhatSchwabAccepts(t *testing.T) {
	for in, want := range map[int]int{-1: 1, 0: 1, 1: 1, 2: 2, 3: 3, 4: 5, 5: 5, 6: 10, 10: 10, 11: 15, 16: 20, 20: 20, 99: 20} {
		if got := PeriodYears(in); got != want {
			t.Errorf("PeriodYears(%d) = %d, want %d", in, got, want)
		}
	}
}

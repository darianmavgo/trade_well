package gridsearch

import "testing"

func TestStopMultiplier(t *testing.T) {
	for in, want := range map[float64]float64{0: 0, -1: 0, 0.05: 0.95, 0.07: 0.93} {
		if got := stopMultiplier(in); got < want-1e-9 || got > want+1e-9 {
			t.Errorf("stopMultiplier(%v) = %v, want %v", in, got, want)
		}
	}
}

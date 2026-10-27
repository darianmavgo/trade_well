package strategy

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
)

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Real GOOGL closes: the bar_sma slice table's averages must equal a plain
// trailing mean of the real closes.
func TestBuildBarSMAOnRealBars(t *testing.T) {
	market := realbars.Copy(t, "GOOGL")
	closes := realbars.Closes(t, market, "GOOGL")
	dates := realbars.Dates(t, market, "GOOGL")
	if len(closes) < 260 {
		t.Skipf("GOOGL has only %d bars", len(closes))
	}
	db, err := OpenCalcDB(market, filepath.Join(t.TempDir(), "calc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BuildBarSMA(db, []string{"GOOGL"}); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{49, 199, 200, 500, len(closes) - 1} {
		var r struct {
			SMA50  float64 `db:"sma50"`
			SMA200 float64 `db:"sma200"`
		}
		if err := db.Get(&r, "SELECT sma50, sma200 FROM bar_sma WHERE symbol = 'GOOGL' AND date = ?", dates[i]); err != nil {
			t.Fatal(err)
		}
		want50, want200 := mean(closes[i-49:i+1]), mean(closes[maxInt(0, i-199):i+1])
		if math.Abs(r.SMA50-want50) > 1e-9 || math.Abs(r.SMA200-want200) > 1e-9 {
			t.Fatalf("%s: SMA50 %v want %v, SMA200 %v want %v", dates[i], r.SMA50, want50, r.SMA200, want200)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

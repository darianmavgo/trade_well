package study

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

func TestOLSLineAndMedian(t *testing.T) {
	slope, icpt, r2 := olsLine([]float64{1, 2, 3, 4}, []float64{3, 5, 7, 9})
	if math.Abs(slope-2) > 1e-9 || math.Abs(icpt-1) > 1e-9 || math.Abs(r2-1) > 1e-9 {
		t.Fatalf("slope=%v icpt=%v r2=%v", slope, icpt, r2)
	}
	if median([]float64{5, 1, 3}) != 3 || median([]float64{1, 2, 3, 4}) != 2.5 {
		t.Fatal("median wrong")
	}
}

// TQQQ volume built as exactly 3x QQQ volume (times a tiny independent wobble)
// must show ~1.0 change-correlation, ~1.0 elasticity and a ~3x share ratio.
func TestQQQTQQQVolumeStudy_KnownRelationship(t *testing.T) {
	dir := t.TempDir()
	mdbPath := filepath.Join(dir, "market.db")
	db, err := storage.OpenSQLite(mdbPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := uint32(7)
	rnd := func() float64 { seed = seed*1664525 + 1013904223; return float64(seed%10000) / 10000 }
	day := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var bars []models.Bar
	for i := 0; i < 400; i++ {
		d := day.AddDate(0, 0, i).Format("2006-01-02")
		qv := 1e6 * (1 + rnd())
		bars = append(bars,
			models.Bar{Symbol: "QQQ", Date: d, Timeframe: "1d", Open: 100, High: 101, Low: 99, Close: 100 + float64(i%7), Volume: int64(qv)},
			models.Bar{Symbol: "TQQQ", Date: d, Timeframe: "1d", Open: 50, High: 51, Low: 49, Close: 50 + float64(i%5), Volume: int64(3 * qv * (1 + 0.001*rnd()))},
		)
	}
	if err := storage.UpsertBars(db, "backtest_start", bars); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := &QQQTQQQVolumeStudy{}
	out := filepath.Join(dir, "out", "qqq_tqqq_volume.db")
	s.SetDatabases(mdbPath, out)
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}

	rdb, err := sqlx.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	get := func(metric string) float64 {
		var v float64
		if err := rdb.Get(&v, `SELECT value FROM volume_summary WHERE metric = ?`, metric); err != nil {
			t.Fatalf("%s: %v", metric, err)
		}
		return v
	}
	if c := get("corr_daily_volume_change"); c < 0.99 {
		t.Errorf("corr = %v, want ~1", c)
	}
	if e := get("elasticity_tqqq_on_qqq"); math.Abs(e-1) > 0.02 {
		t.Errorf("elasticity = %v, want ~1", e)
	}
	if r := get("median_share_volume_ratio"); math.Abs(r-3) > 0.05 {
		t.Errorf("share ratio = %v, want ~3", r)
	}
	var lags int
	if err := rdb.Get(&lags, `SELECT COUNT(*) FROM volume_lag_corr`); err != nil || lags != 11 {
		t.Errorf("lag rows = %d (%v), want 11", lags, err)
	}
	var top float64
	_ = rdb.Get(&top, `SELECT corr FROM volume_lag_corr WHERE lag_days = 0`)
	if top < 0.99 {
		t.Errorf("lag-0 corr = %v", top)
	}
}

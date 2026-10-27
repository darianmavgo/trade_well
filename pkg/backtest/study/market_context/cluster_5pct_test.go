package market_context

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestCluster5PctUsesPriorRegime(t *testing.T) {
	if _, ok := study.Get("cluster_5pct"); !ok {
		t.Fatal("cluster_5pct is not registered")
	}

	dir := t.TempDir()
	marketPath := filepath.Join(dir, "market.db")
	clusterPath := filepath.Join(dir, "clusters.db")
	freqPath := filepath.Join(dir, "gain_5pct_frequency.db")
	resultsPath := filepath.Join(dir, "out.db")

	market, err := sqlx.Open("sqlite", marketPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := market.Exec(`CREATE TABLE backtest_start (Date TEXT, timeframe TEXT, close REAL, symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	// After cluster 0 the next close is +6%. After cluster 1 it is -6%.
	// Day 0 is the warmup close and is not labeled.
	closes := []float64{100, 101, 107.06, 100.6364, 106.674584, 100.27410904}
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05", "2024-01-08"}
	clusters := []int{0, 0, 1, 0, 1, 0}
	for i, date := range dates {
		if _, err := market.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES (?, '1d', ?, 'AAA')`, date, closes[i]); err != nil {
			t.Fatal(err)
		}
		// A cluster feature ticker with a 5% move must not enter the event table.
		if _, err := market.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES (?, '1d', ?, 'USO')`, date, closes[i]*2); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			continue
		}
		// labeled below
		_ = clusters
	}
	market.Close()

	clustersDB, err := sqlx.Open("sqlite", clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clustersDB.Exec(`CREATE TABLE cluster_day (date TEXT, cluster INTEGER);
		CREATE TABLE cluster_features (ticker TEXT);`); err != nil {
		t.Fatal(err)
	}
	for _, ticker := range []string{"IEF", "GLD", "USO"} {
		if _, err := clustersDB.Exec(`INSERT INTO cluster_features (ticker) VALUES (?)`, ticker); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(dates); i++ {
		if _, err := clustersDB.Exec(`INSERT INTO cluster_day (date, cluster) VALUES (?, ?)`, dates[i], clusters[i]); err != nil {
			t.Fatal(err)
		}
	}
	clustersDB.Close()

	freq, err := sqlx.Open("sqlite", freqPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := freq.Exec(`CREATE TABLE gain_5pct_frequency (symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"AAA", "USO"} {
		if _, err := freq.Exec(`INSERT INTO gain_5pct_frequency (symbol) VALUES (?)`, sym); err != nil {
			t.Fatal(err)
		}
	}
	freq.Close()

	s := &Cluster5Pct{}
	s.SetDatabases(marketPath, resultsPath)
	s.SetClusterDB(clusterPath)
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}

	out, err := sqlx.Open("sqlite", resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	var uso int
	if err := out.Get(&uso, `SELECT COUNT(*) FROM cluster_5pct_day WHERE symbol = 'USO'`); err != nil {
		t.Fatal(err)
	}
	if uso != 0 {
		t.Fatalf("USO event rows = %d, want 0", uso)
	}

	type summary struct {
		Cluster  int     `db:"cluster"`
		Days     int     `db:"symbol_days"`
		GainRate float64 `db:"gain_rate"`
		DropRate float64 `db:"drop_rate"`
		GainLift float64 `db:"gain_lift"`
		DropLift float64 `db:"drop_lift"`
	}
	var rows []summary
	if err := out.Select(&rows, `SELECT cluster, symbol_days, gain_rate, drop_rate, gain_lift, drop_lift FROM cluster_5pct_summary WHERE timing = 'next_day' ORDER BY cluster`); err != nil {
		t.Fatal(err)
	}
	by := map[int]summary{}
	for _, r := range rows {
		by[r.Cluster] = r
	}
	if by[0].Days != 2 || math.Abs(by[0].GainRate-1) > 1e-9 || by[0].DropRate != 0 {
		t.Fatalf("next-day cluster 0 = %+v, want 2 days all +5%%", by[0])
	}
	if by[1].Days != 2 || by[1].GainRate != 0 || math.Abs(by[1].DropRate-1) > 1e-9 {
		t.Fatalf("next-day cluster 1 = %+v, want 2 days all -5%%", by[1])
	}
	if math.Abs(by[0].GainLift-2) > 1e-9 {
		t.Fatalf("cluster 0 gain lift = %v, want 2", by[0].GainLift)
	}
	if math.Abs(by[1].DropLift-2) > 1e-9 {
		t.Fatalf("cluster 1 drop lift = %v, want 2", by[1].DropLift)
	}
}

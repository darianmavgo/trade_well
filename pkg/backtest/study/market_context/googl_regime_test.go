package market_context

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestGooglRegimeBetaSplit(t *testing.T) {
	if _, ok := study.Get("googl_market_context"); !ok {
		t.Fatal("googl_market_context is not registered")
	}

	dir := t.TempDir()
	marketPath := filepath.Join(dir, "market.db")
	clusterPath := filepath.Join(dir, "clusters.db")
	resultsPath := filepath.Join(dir, "out.db")

	market, err := sqlx.Open("sqlite", marketPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := market.Exec(`CREATE TABLE backtest_start (
		Date TEXT, timeframe TEXT, close REAL, symbol TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	// Warmup close, then three identical-path days (cluster 0, beta 1)
	// and three days where GOOGL's return is twice VOO's (cluster 1, beta 2).
	type px struct {
		date       string
		voo, googl float64
		cluster    int
		labeled    bool
	}
	days := []px{
		{"2024-01-01", 100, 100, 0, false},
		{"2024-01-02", 125, 125, 0, true},
		{"2024-01-03", 93.75, 93.75, 0, true},
		{"2024-01-04", 117.1875, 117.1875, 0, true},
		{"2024-01-05", 146.484375, 175.78125, 1, true},
		{"2024-01-08", 109.86328125, 87.890625, 1, true},
		{"2024-01-09", 137.3291015625, 131.8359375, 1, true},
	}
	for _, d := range days {
		for _, sym := range []struct {
			name string
			px   float64
		}{{"VOO", d.voo}, {"GOOGL", d.googl}, {"SPY", 50}} {
			if _, err := market.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES (?, '1d', ?, ?)`,
				d.date, sym.px, sym.name); err != nil {
				t.Fatal(err)
			}
		}
	}
	market.Close()

	clusters, err := sqlx.Open("sqlite", clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clusters.Exec(`CREATE TABLE cluster_day (date TEXT, cluster INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if !d.labeled {
			continue
		}
		if _, err := clusters.Exec(`INSERT INTO cluster_day (date, cluster) VALUES (?, ?)`, d.date, d.cluster); err != nil {
			t.Fatal(err)
		}
	}
	// A labeled day with no bars, and the warmup day, must not produce a row.
	if _, err := clusters.Exec(`INSERT INTO cluster_day (date, cluster) VALUES ('2024-02-01', 0)`); err != nil {
		t.Fatal(err)
	}
	clusters.Close()

	s := &GooglRegime{}
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

	var n int
	if err := out.Get(&n, `SELECT COUNT(*) FROM googl_regime_day`); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("regime days = %d, want 6", n)
	}

	type summary struct {
		Cluster   int     `db:"cluster"`
		N         int     `db:"n"`
		Beta      float64 `db:"beta"`
		Alpha     float64 `db:"alpha_ann"`
		Residual  float64 `db:"mean_residual_full_beta"`
		DownShare float64 `db:"share_googl_down_when_voo_down"`
		DownMean  float64 `db:"mean_googl_when_voo_down"`
	}
	var rows []summary
	if err := out.Select(&rows, `SELECT cluster, n, beta, alpha_ann, mean_residual_full_beta, share_googl_down_when_voo_down, mean_googl_when_voo_down FROM googl_regime_summary ORDER BY cluster`); err != nil {
		t.Fatal(err)
	}
	by := map[int]summary{}
	for _, r := range rows {
		by[r.Cluster] = r
	}
	assertBeta := func(cluster int, wantN int, wantBeta float64) {
		t.Helper()
		r, ok := by[cluster]
		if !ok {
			t.Fatalf("missing cluster %d", cluster)
		}
		if r.N != wantN {
			t.Fatalf("cluster %d n=%d, want %d", cluster, r.N, wantN)
		}
		if math.Abs(r.Beta-wantBeta) > 1e-6 {
			t.Fatalf("cluster %d beta=%v, want %v", cluster, r.Beta, wantBeta)
		}
		if math.Abs(r.Alpha) > 1e-6 {
			t.Fatalf("cluster %d alpha=%v, want 0", cluster, r.Alpha)
		}
	}
	assertBeta(0, 3, 1)
	assertBeta(1, 3, 2)
	if by[-1].N != 6 {
		t.Fatalf("full sample n=%d", by[-1].N)
	}
	if math.Abs(by[0].DownMean-(-0.25)) > 1e-6 || math.Abs(by[0].DownShare-1) > 1e-9 {
		t.Fatalf("cluster 0 down-day capture = mean %v share %v", by[0].DownMean, by[0].DownShare)
	}
	if math.Abs(by[1].DownMean-(-0.5)) > 1e-6 || math.Abs(by[1].DownShare-1) > 1e-9 {
		t.Fatalf("cluster 1 down-day capture = mean %v share %v", by[1].DownMean, by[1].DownShare)
	}

	// Residual uses the full-sample beta, so cluster 1 is not zero even though its own alpha is.
	if math.Abs(by[1].Residual) < 1e-6 {
		t.Fatalf("cluster 1 full-sample residual should not collapse into its own beta, got %v", by[1].Residual)
	}
}

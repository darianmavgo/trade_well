package market_context

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Cluster5Pct tests whether the oil, gold, and bond regimes line up with
// 5% up days and 5% down days for the tickers in gain_5pct_frequency.
// The prediction row uses the prior session's cluster. The same-day row is
// only the contemporaneous rate.
type Cluster5Pct struct {
	marketDBPath  string
	resultsDBPath string
	clusterDBPath string
}

func init() { study.Register(&Cluster5Pct{}) }

func (s *Cluster5Pct) ID() string { return "cluster_5pct" }

func (s *Cluster5Pct) Name() string { return "Oil, Gold, Bonds vs 5% Days" }

func (s *Cluster5Pct) Description() string {
	return "Next-day and same-day rates of 5% gains and 5% declines by the IEF, GLD, USO cluster, for tickers in data/gain_5pct_frequency.db."
}

func (s *Cluster5Pct) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath, s.resultsDBPath = marketDBPath, resultsDBPath
}

func (s *Cluster5Pct) SetClusterDB(path string) { s.clusterDBPath = path }

func (s *Cluster5Pct) Run() error {
	log.Printf("Running study: %s", s.Name())
	if s.clusterDBPath == "" {
		return fmt.Errorf("cluster database is required (IEF, GLD, USO cluster_day)")
	}
	if err := s.requireOilGoldBondClusters(); err != nil {
		return err
	}
	freqPath := filepath.Join(filepath.Dir(s.marketDBPath), "gain_5pct_frequency.db")
	if _, err := os.Stat(freqPath); err != nil {
		return fmt.Errorf("gain frequency database %s: %w", freqPath, err)
	}
	query, err := loadStudySQL("cluster_5pct.sql")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	db, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("open results db: %w", err)
	}
	defer db.Close()

	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS market;", sqlQuote(s.marketDBPath))); err != nil {
		return fmt.Errorf("attach market database: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS clusters;", sqlQuote(s.clusterDBPath))); err != nil {
		return fmt.Errorf("attach cluster database: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS freq;", sqlQuote(freqPath))); err != nil {
		return fmt.Errorf("attach gain frequency database: %w", err)
	}
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("cluster 5%% query: %w", err)
	}

	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM cluster_5pct_day`); err != nil {
		return fmt.Errorf("count event days: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no 5%% event rows; check that gain_5pct_frequency symbols exist in the market database")
	}

	type row struct {
		Timing     string  `db:"timing"`
		Cluster    int     `db:"cluster"`
		SymbolDays int     `db:"symbol_days"`
		Tickers    int     `db:"tickers"`
		GainDays   int     `db:"gain_days"`
		DropDays   int     `db:"drop_days"`
		GainRate   float64 `db:"gain_rate"`
		DropRate   float64 `db:"drop_rate"`
		GainLift   float64 `db:"gain_lift"`
		DropLift   float64 `db:"drop_lift"`
	}
	var rows []row
	if err := db.Select(&rows, `
		SELECT timing, cluster, symbol_days, tickers, gain_days, drop_days,
		       gain_rate, drop_rate, gain_lift, drop_lift
		FROM cluster_5pct_summary
		ORDER BY timing, cluster`); err != nil {
		return fmt.Errorf("read summary: %w", err)
	}
	for _, r := range rows {
		label := fmt.Sprintf("cluster %d", r.Cluster)
		if r.Cluster < 0 {
			label = "baseline"
		}
		log.Printf("%s %s: days=%d tickers=%d  5%% up %d (%.2f%%, lift %.2f)  5%% down %d (%.2f%%, lift %.2f)",
			r.Timing, label, r.SymbolDays, r.Tickers, r.GainDays, r.GainRate*100, r.GainLift, r.DropDays, r.DropRate*100, r.DropLift)
	}
	log.Printf("Results saved to %s", s.resultsDBPath)
	return nil
}

// requireOilGoldBondClusters rejects the five-asset and no-VOO fits. Those
// still include HYG or VOO. This study is the IEF, GLD, USO labeling.
func (s *Cluster5Pct) requireOilGoldBondClusters() error {
	if _, err := os.Stat(s.clusterDBPath); err != nil {
		return fmt.Errorf("cluster database %s: %w", s.clusterDBPath, err)
	}
	db, err := sqlx.Open("sqlite", "file:"+s.clusterDBPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'cluster_features'`); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s has no cluster_features table; pass -cluster-db data/reports/market_context_20d_nohyg.db", s.clusterDBPath)
	}
	var features []string
	if err := db.Select(&features, `SELECT ticker FROM cluster_features ORDER BY ticker`); err != nil {
		return err
	}
	want := map[string]bool{"GLD": true, "IEF": true, "USO": true}
	if len(features) != len(want) {
		return fmt.Errorf("cluster features are %v; this study wants IEF, GLD, USO only (data/reports/market_context_20d_nohyg.db)", features)
	}
	for _, f := range features {
		if !want[f] {
			return fmt.Errorf("cluster features are %v; this study wants IEF, GLD, USO only", features)
		}
	}
	return nil
}

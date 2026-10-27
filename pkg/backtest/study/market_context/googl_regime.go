package market_context

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// GooglRegime measures GOOGL's daily beta and residual versus VOO inside
// each market-context cluster. Cluster labels are read from cluster_day;
// this study does not refit them.
type GooglRegime struct {
	marketDBPath  string
	resultsDBPath string
	clusterDBPath string
}

func init() { study.Register(&GooglRegime{}) }

func (s *GooglRegime) ID() string { return "googl_market_context" }

func (s *GooglRegime) Name() string { return "GOOGL Inside Market-Context Regimes" }

func (s *GooglRegime) Description() string {
	return "Daily GOOGL beta to VOO, Jensen alpha, and full-sample residual, split by cluster_day from market_context_20d."
}

func (s *GooglRegime) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath, s.resultsDBPath = marketDBPath, resultsDBPath
}

func (s *GooglRegime) SetClusterDB(path string) { s.clusterDBPath = path }

func (s *GooglRegime) Run() error {
	log.Printf("Running study: %s", s.Name())
	if s.clusterDBPath == "" {
		return fmt.Errorf("cluster database is required (cluster_day)")
	}
	if _, err := os.Stat(s.clusterDBPath); err != nil {
		return fmt.Errorf("cluster database %s: %w", s.clusterDBPath, err)
	}
	query, err := loadStudySQL("googl_regime.sql")
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
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("googl regime query: %w", err)
	}

	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM googl_regime_day`); err != nil {
		return fmt.Errorf("count regime days: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no overlapping GOOGL and VOO days in %s; run `market_history GOOGL, VOO` into the market database first", s.clusterDBPath)
	}

	type row struct {
		Cluster      int     `db:"cluster"`
		N            int     `db:"n"`
		Start        string  `db:"start_date"`
		End          string  `db:"end_date"`
		MeanGoogl    float64 `db:"mean_googl"`
		MeanVOO      float64 `db:"mean_voo"`
		Beta         float64 `db:"beta"`
		AlphaAnn     float64 `db:"alpha_ann"`
		Residual     float64 `db:"mean_residual_full_beta"`
		VooDown      int     `db:"voo_down_days"`
		MeanWhenDown float64 `db:"mean_googl_when_voo_down"`
		ShareDown    float64 `db:"share_googl_down_when_voo_down"`
	}
	var rows []row
	if err := db.Select(&rows, `
		SELECT cluster, n, start_date, end_date, mean_googl, mean_voo, beta, alpha_ann,
		       mean_residual_full_beta, voo_down_days, mean_googl_when_voo_down, share_googl_down_when_voo_down
		FROM googl_regime_summary
		ORDER BY cluster`); err != nil {
		return fmt.Errorf("read summary: %w", err)
	}
	for _, r := range rows {
		label := fmt.Sprintf("cluster %d", r.Cluster)
		if r.Cluster < 0 {
			label = "full sample"
		}
		log.Printf("%s: n=%d %s..%s  beta=%.3f  alpha_ann=%.3f  residual=%.5f  VOO-down days=%d mean GOOGL=%.5f share down=%.3f",
			label, r.N, r.Start, r.End, r.Beta, r.AlphaAnn, r.Residual, r.VooDown, r.MeanWhenDown, r.ShareDown)
	}
	log.Printf("Results saved to %s", s.resultsDBPath)
	return nil
}

func sqlQuote(path string) string { return strings.ReplaceAll(path, "'", "''") }

func loadStudySQL(name string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "sql", "studies", name)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	q := strings.TrimSpace(string(b))
	if q == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return q, nil
}

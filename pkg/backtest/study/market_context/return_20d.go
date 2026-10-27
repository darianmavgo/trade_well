// Package market_context holds studies of the cross-asset backdrop
// (equities, bonds, gold, oil, credit) used alongside a trading strategy.
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

// Symbols is the market-context basket. 20dayreturn.sql filters to the same set.
var Symbols = []string{"VOO", "IEF", "GLD", "USO", "HYG"}

// Return20d writes 20-session simple and log returns for Symbols.
type Return20d struct {
	marketDBPath  string
	resultsDBPath string
}

func init() { study.Register(&Return20d{}) }

func (s *Return20d) ID() string { return "market_context_20d" }

func (s *Return20d) Name() string { return "Market Context 20-Day Returns" }

func (s *Return20d) Description() string {
	return "20-session simple and log returns for VOO, IEF, GLD, USO, and HYG (sql/studies/20dayreturn.sql)."
}

func (s *Return20d) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath, s.resultsDBPath = marketDBPath, resultsDBPath
}

func (s *Return20d) Run() error {
	log.Printf("Running study: %s", s.Name())
	query, err := loadReturn20dSQL()
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

	attach := strings.ReplaceAll(s.marketDBPath, "'", "''")
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS market;", attach)); err != nil {
		return fmt.Errorf("attach market database: %w", err)
	}
	if _, err := db.Exec(ohlcvViewSQL); err != nil {
		return fmt.Errorf("build ohlcv view: %w", err)
	}

	stmt := "DROP TABLE IF EXISTS return_20d; CREATE TABLE return_20d AS " + query
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("calculate 20-day returns: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_return_20d_ticker_date ON return_20d(ticker, date);`); err != nil {
		return fmt.Errorf("index return_20d: %w", err)
	}

	type countRow struct {
		Ticker string `db:"ticker"`
		N      int    `db:"n"`
		Filled int    `db:"filled"`
	}
	var counts []countRow
	if err := db.Select(&counts, `
		SELECT ticker,
		       COUNT(*) AS n,
		       SUM(CASE WHEN return_20d_simple IS NOT NULL THEN 1 ELSE 0 END) AS filled
		FROM return_20d
		GROUP BY ticker
		ORDER BY ticker`); err != nil {
		return fmt.Errorf("summarize return_20d: %w", err)
	}
	have := map[string]countRow{}
	for _, c := range counts {
		have[c.Ticker] = c
	}
	var missing []string
	for _, sym := range Symbols {
		c, ok := have[sym]
		if !ok || c.Filled == 0 {
			missing = append(missing, sym)
			continue
		}
		log.Printf("%s: %d daily bars, %d with a 20-session return", sym, c.N, c.Filled)
	}
	if len(missing) > 0 {
		return fmt.Errorf("no 20-session returns for %s; run `market_history %s` first",
			strings.Join(missing, ", "), strings.Join(Symbols, ", "))
	}
	log.Printf("Results saved to %s", s.resultsDBPath)
	return nil
}

// ohlcvViewSQL adapts daily backtest_start bars to the columns 20dayreturn.sql expects.
const ohlcvViewSQL = `
CREATE TEMP VIEW IF NOT EXISTS ohlcv AS
SELECT symbol AS ticker,
       substr(Date, 1, 10) AS date,
       close
FROM market.backtest_start
WHERE timeframe = '1d'
  AND length(Date) = 10
  AND close > 0
  AND symbol IN ('VOO', 'IEF', 'GLD', 'USO', 'HYG');
`

// loadReturn20dSQL reads sql/studies/20dayreturn.sql from the repo that contains
// this process's working directory, walking up to go.mod so `go test` from the
// package directory still finds it.
func loadReturn20dSQL() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "sql", "studies", "20dayreturn.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	q := strings.TrimSpace(string(b))
	q = strings.TrimSuffix(q, ";")
	if q == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return q, nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

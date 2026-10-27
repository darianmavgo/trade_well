package scan_market

// Reads the Markov model ASSET (markov_models.db, built in backtestgosqlite), read-only.

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// CopyMarkovModelAssetPredictionsToTurso is SavePredictions for a given model asset and symbol list: it reads
// the Markov model ASSET (markov_models.db, built in backtestgosqlite) read-only and writes Turso.
func CopyMarkovModelAssetPredictionsToTurso(db *sql.DB, modelDB, scanRunID string, symbols []string, now time.Time) (int, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	if _, err := os.Stat(modelDB); err != nil {
		return 0, nil
	}
	if err := EnsureSchema(db); err != nil {
		return 0, err
	}
	m, err := storage.OpenSQLiteReadOnly(modelDB)
	if err != nil {
		return 0, nil
	}
	defer m.Close()
	sort.Strings(symbols)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(symbols)), ",")
	args := make([]any, len(symbols))
	for i, s := range symbols {
		args[i] = s
	}
	rows, err := m.Query(`SELECT p.symbol, p.date, p.state, p.prob_bull, p.prob_bear, p.signal, COALESCE(mm.trained_through, '')
		FROM markov_prediction p JOIN markov_model_meta mm ON mm.symbol = p.symbol
		WHERE p.symbol IN (`+marks+`) AND mm.trained_through IS NOT NULL AND mm.trained_through <> '' AND p.date > mm.trained_through`, args...)
	if err != nil {
		return 0, fmt.Errorf("read live predictions: %w", err)
	}
	defer rows.Close()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for rows.Next() {
		var sym, date, through string
		var state int
		var bull, bear, sig float64
		if err := rows.Scan(&sym, &date, &state, &bull, &bear, &sig, &through); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO markov_prediction_live (symbol, date, state, prob_bull, prob_bear, signal, trained_through, scan_run_id, saved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, sym, date, state, bull, bear, sig, through, scanRunID, now.UTC().Format(time.RFC3339)); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

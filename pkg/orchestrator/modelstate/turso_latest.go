package modelstate

import (
	"context"
	"database/sql"
	"fmt"
)

// HMMTip is the newest hidden Markov prediction stored in Turso for one model.
type HMMTip struct {
	Date           string // session the regime is as of (its close)
	Regime         int
	RegimeProb     float64
	NextRegime     int // the regime for the session after Date, which the strategies trade on
	NextRegimeProb float64
}

// LatestHMMPredictionInTurso returns the newest prediction of symbol's model (keyed by trainedAt) that Prepare has
// persisted to Turso. It returns sql.ErrNoRows when nothing is stored for that model.
func LatestHMMPredictionInTurso(ctx context.Context, db *sql.DB, symbol, trainedAt string) (HMMTip, error) {
	var t HMMTip
	if err := ensureHMMSchema(db); err != nil {
		return t, err
	}
	err := db.QueryRowContext(ctx, `SELECT date, regime, regime_prob, next_regime, next_regime_prob
		FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ? ORDER BY date DESC LIMIT 1`, symbol, trainedAt).
		Scan(&t.Date, &t.Regime, &t.RegimeProb, &t.NextRegime, &t.NextRegimeProb)
	if err != nil && err != sql.ErrNoRows {
		return t, fmt.Errorf("modelstate: newest %s prediction: %w", symbol, err)
	}
	return t, err
}

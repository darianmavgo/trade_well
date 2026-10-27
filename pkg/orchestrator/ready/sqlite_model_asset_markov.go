package ready

// Reads the Markov model ASSET (markov_models.db, built in backtestgosqlite), read-only.

import (
	"database/sql"

	"github.com/darianmavgo/trade_well/pkg/backtest/markov_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func readMarkovModelAssetTrainedThrough(path, sym string) string {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return "unknown"
	}
	defer db.Close()
	var t sql.NullString
	if err := db.Get(&t, "SELECT trained_through FROM markov_model_meta WHERE symbol = ?", sym); err != nil || !t.Valid {
		return "unknown"
	}
	return t.String
}

// readMarkovModelAssetLastDate is the last date the Markov model asset at path has a prediction
// for symbol, and whether the asset has a model for it.
func readMarkovModelAssetLastDate(path, symbol string) (string, bool) {
	return markov_strategy.ModelLastDate(path, symbol)
}

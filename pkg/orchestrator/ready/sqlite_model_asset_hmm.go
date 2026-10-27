package ready

// Reads the hidden Markov model ASSET (hmm_models.db, built in backtestgosqlite), read-only.

import (
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

// readHMMModelAssetLatest is the newest prediction of the hidden Markov model asset at path for symbol.
func readHMMModelAssetLatest(path, symbol string) (train.HMMLatest, error) {
	return train.ReadHMMLatest(path, symbol)
}

package ready

// Reads the market WORKING COPY (the local market file, rebuilt from Turso market_bars).

import (
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// readMarketWorkingCopyLastDate is the newest daily bar of symbol in the market working copy at path.
func readMarketWorkingCopyLastDate(path, symbol string) (string, error) {
	return storage.SymbolLastDate(path, symbol)
}

package live_vs_backtest_trades

// Reads the market WORKING COPY (the local market file, rebuilt from Turso market_bars).

import (
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// LoadBarsFromMarketWorkingCopyFromMarketWorkingCopy reads table from the market WORKING COPY (the local market file rebuilt from Turso market_bars). An empty path or a missing
// file returns a nil map and a note; the live side of a reconciliation can
// still be written. A database that opens and then fails its query is an error.
func LoadBarsFromMarketWorkingCopy(path, table string) (map[string][]models.Bar, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "market db not set; model_outcome is NO_BARS", nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Sprintf("market db %s is missing; model_outcome is NO_BARS", path), nil
	}
	market, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, "", err
	}
	defer market.Close()
	bars, _, err := storage.FetchBars(market, table, nil, "", "")
	if err != nil {
		return nil, "", err
	}
	return bars, "", nil
}

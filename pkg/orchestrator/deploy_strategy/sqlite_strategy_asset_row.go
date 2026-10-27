package deploy_strategy

import (
	"fmt"
	"os"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// findStrategyAssetRowTable returns the table that holds id as a strategy definition row in the
// strategy ASSET at refPath: backtestgosqlite's refdata/strategies.db, opened read-only.
func findStrategyAssetRowTable(refPath, id string) (string, error) {
	if _, err := os.Stat(refPath); err != nil {
		return "", fmt.Errorf("%w (the bundle is exported from the strategies database of the sibling backtestgosqlite checkout)", err)
	}
	db, err := refdb.OpenExisting(refPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	for _, t := range rowTables {
		if got, ok := refdb.ExactID(db, t, id); ok && got == id {
			return t, nil
		}
	}
	return "", fmt.Errorf("%q is not a strategy row in %s: only row-backed strategies can be exported into the bundle", id, refPath)
}

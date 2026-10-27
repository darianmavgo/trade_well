package strategy

import (
	"regexp"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

var barSMASymbol = regexp.MustCompile(`^[A-Za-z0-9.^=_-]+$`)

// OpenCalcDB opens the calc database with the market database attached and
// exposed as the backtest_start view, the setup every SQL stage expects.
func OpenCalcDB(marketDBPath, calcDBPath string) (*sqlx.DB, error) {
	return openAttachedCalcDB(marketDBPath, calcDBPath)
}

// SQLSymbolList quotes symbols for an IN (...) list in a stage. Names with
// characters outside letters, digits and . ^ = _ - are skipped. It returns ""
// when nothing is left.
func SQLSymbolList(symbols []string) string {
	var quoted []string
	for _, sym := range symbols {
		if barSMASymbol.MatchString(sym) {
			quoted = append(quoted, "'"+sym+"'")
		}
	}
	sort.Strings(quoted)
	return strings.Join(quoted, ",")
}

// BuildBarSMA runs the sql/stages/bar_sma stage in db, filling the bar_sma slice
// table (50 and 200 bar simple averages of close over each symbol's full history).
func BuildBarSMA(db *sqlx.DB, symbols []string) error {
	list := SQLSymbolList(symbols)
	if list == "" {
		return nil
	}
	return storage.RunStage(db, "bar_sma", map[string]string{"__SYMBOL_LIST__": list})
}

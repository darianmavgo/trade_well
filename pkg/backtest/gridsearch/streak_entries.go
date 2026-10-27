package gridsearch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// streakEntryKey names one entry set: the entries depend on the trade symbol,
// the streak length and the regime, not on hold days, take-profit or stop.
type streakEntryKey struct {
	sym     string
	sigDays int
	regime  string
}

var safeSQLWord = regexp.MustCompile(`^[A-Za-z0-9.^=_<>+ -]+$`)

// streakRegimePredicate is the watch-bar test for a regime label, with the
// same suffix rules the Go grid search always used: an average of 0 never
// filters, and any other label means "All Regimes".
func streakRegimePredicate(regime string) string {
	switch {
	case strings.HasSuffix(regime, "<SMA200"):
		return "v.sma200 <= 0 OR v.close < v.sma200"
	case strings.HasSuffix(regime, "<SMA50"):
		return "v.sma50 <= 0 OR v.close < v.sma50"
	case strings.HasSuffix(regime, ">=SMA200"):
		return "v.sma200 <= 0 OR v.close >= v.sma200"
	case strings.HasSuffix(regime, ">=SMA50"):
		return "v.sma50 <= 0 OR v.close >= v.sma50"
	default:
		return "1=1"
	}
}

// buildStreakEntries calculates, in SQL, every streak entry the sweep's grid
// needs. The slice tables (bar_sma, streak_slice, streak_entries) live in a
// calc database that is removed afterwards unless runner.KeepCalc is set.
// Go only reads the entry rows back as signals without exits.
func buildStreakEntries(marketDB, id, signalSym, direction, startDate, endDate string, syms []string, days []int, regimes []string) (map[streakEntryKey][]models.Signal, error) {
	if marketDB == "" {
		return nil, fmt.Errorf("streak grid search needs the market database path")
	}
	calcPath, cleanup := runner.CalcDBPath(appenv.Reports(), "gridsearch_"+id)
	defer cleanup()

	db, err := strategy.OpenCalcDB(marketDB, calcPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := strategy.BuildBarSMA(db, []string{signalSym}); err != nil {
		return nil, err
	}
	if !safeSQLWord.MatchString(signalSym) {
		return nil, fmt.Errorf("unsafe signal symbol %q", signalSym)
	}
	start, end := startDate, endDate
	if start == "" {
		start = "0000-00-00"
	}
	if end == "" {
		end = "9999-12-31"
	}
	if err := storage.RunStage(db, "streak_slice", map[string]string{
		"__SIGNAL_SYMBOL__": signalSym, "__START_DATE__": start, "__END_DATE__": end,
	}); err != nil {
		return nil, err
	}

	col := "down_streak"
	if direction == "rally" {
		col = "up_streak"
	}
	out := make(map[streakEntryKey][]models.Signal)
	for _, sym := range syms {
		if !safeSQLWord.MatchString(sym) {
			return nil, fmt.Errorf("unsafe trade symbol %q", sym)
		}
		for _, d := range days {
			for _, regime := range regimes {
				if !safeSQLWord.MatchString(regime) {
					return nil, fmt.Errorf("unsafe regime %q", regime)
				}
				if err := storage.RunStage(db, "streak_entry", map[string]string{
					"__TRADE_SYMBOL__": sym, "__STREAK_COL__": col, "__SIGNAL_DAYS__": fmt.Sprint(d),
					"__REGIME_LABEL__": regime, "__REGIME_PREDICATE__": streakRegimePredicate(regime),
				}); err != nil {
					return nil, err
				}
			}
		}
	}

	var rows []struct {
		Sym    string  `db:"trade_symbol"`
		Days   int     `db:"signal_days"`
		Regime string  `db:"regime"`
		Date   string  `db:"date"`
		Open   float64 `db:"open"`
		High   float64 `db:"high"`
		Low    float64 `db:"low"`
		Close  float64 `db:"close"`
		Volume int64   `db:"volume"`
	}
	if err := db.Select(&rows, `SELECT trade_symbol, signal_days, regime, date, open, high, low, close, volume
		FROM streak_entries ORDER BY trade_symbol, signal_days, regime, date`); err != nil {
		return nil, err
	}
	for _, r := range rows {
		k := streakEntryKey{r.Sym, r.Days, r.Regime}
		out[k] = append(out[k], models.Signal{
			Symbol: r.Sym, Date: r.Date, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close,
			Volume: r.Volume, Entry: 1, Direction: "LONG", OrderType: "limit", BuyLimit: r.Close,
			Regime: r.Regime, AssetClass: "equity",
		})
	}
	return out, nil
}

// streakSignalsFor copies the entries of one grid point and prices its exits:
// take-profit and stop as absolute prices (Rule 5), 0 meaning "use config".
func streakSignalsFor(entries []models.Signal, tp, sl float64, hold int, strategyID string) []models.Signal {
	sigs := make([]models.Signal, len(entries))
	for i, e := range entries {
		e.StrategyID = strategyID
		e.HoldDaysOverride = hold
		if tp > 0 {
			e.TakeProfit = e.Close * (1.0 + tp)
		}
		if sl > 0 {
			e.StopLoss = e.Close * (1.0 - sl)
		}
		sigs[i] = e
	}
	return sigs
}

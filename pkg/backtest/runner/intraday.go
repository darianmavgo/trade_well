package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// LoadIntraday reads the hourly bars (timeframe 1h, stored with a UTC
// "YYYY-MM-DD HH:MM:SS" date) of the symbols from the hourly database beside
// the daily market database (appenv.BarDB). They
// are returned in Eastern time, keeping only the hours that start from 9:00 to
// 15:00 (the 9:00 hour holds the half hour before the open, see
// hoursim.Bars). A database with no hourly bars gives an empty map.
func LoadIntraday(marketDBPath string, symbols []string) (map[string][]models.Bar, error) {
	out := map[string][]models.Bar{}
	if len(symbols) == 0 {
		return out, nil
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(appenv.BarDB(marketDBPath, "1h"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(symbols)), ",")
	args := make([]any, len(symbols))
	for i, s := range symbols {
		args[i] = s
	}
	var rows []models.Bar
	err = db.Select(&rows, fmt.Sprintf(`SELECT symbol, CAST(Date AS TEXT) AS Date, open, high, low, close, volume FROM backtest_start
		WHERE timeframe = '1h' AND length(Date) = 19 AND symbol IN (%s) ORDER BY symbol, Date`, marks), args...)
	if err != nil {
		return nil, err
	}
	for _, b := range rows {
		t, err := time.ParseInLocation("2006-01-02 15:04:05", b.Date, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("hourly bar %s %q: %w", b.Symbol, b.Date, err)
		}
		et := t.In(loc)
		if h := et.Hour(); h < 9 || h > 15 {
			continue
		}
		b.Date = et.Format("2006-01-02 15:04")
		out[b.Symbol] = append(out[b.Symbol], b)
	}
	return out, nil
}

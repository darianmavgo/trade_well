// Package sessions derives daily session bars from hourly bars. Hourly bars are
// the only bars that are downloaded; signals and the simulation clock read
// one row per symbol and trading day, and those rows are built here from the
// regular-session hours (9:00 to 15:00 Eastern, the bars that start in 9 to 15)
// and stored in the same table with a "YYYY-MM-DD" Date and timeframe 1d.
//
// A session bar is exact for the close (the 15:00 bar ends at 16:00), the sum of
// volume and the range of the trades the hourly bars saw. It is approximate in
// one way: the 9:00 hour bar holds the half hour before the 9:30 open, so the
// session open is the 9:00 open and the session high and low can include
// pre-market trades. A session with no trade in an hour simply has no bar for
// that hour, which is correct for a bar built from trades.
package sessions

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

var eastern *time.Location

func init() {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(fmt.Sprintf("sessions: America/New_York: %v", err))
	}
	eastern = loc
}

// Regular reports whether the hourly bar stamped utc ("YYYY-MM-DD HH:MM:SS")
// starts in regular-session hours (9 to 15 Eastern), and the Eastern date.
func Regular(utc string) (date string, ok bool) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", utc, time.UTC)
	if err != nil {
		return "", false
	}
	et := t.In(eastern)
	return et.Format("2006-01-02"), et.Hour() >= 9 && et.Hour() <= 15
}

// Derive builds the session rows of every symbol in table of db from its hourly
// rows. A symbol is only read from two days before its latest session onward, so
// a run on up-to-date data reads almost nothing. It returns the rows written.
func Derive(db *sqlx.DB, table string) (int, error) {
	if table == "" {
		table = "backtest_start"
	}
	if err := storage.ValidateTableName(table); err != nil {
		return 0, err
	}
	if err := storage.EnsureBarTable(db, table); err != nil {
		return 0, err
	}
	var symbols []string
	if err := db.Select(&symbols, fmt.Sprintf(`SELECT DISTINCT symbol FROM %s WHERE timeframe = '1h' ORDER BY symbol`, table)); err != nil {
		return 0, err
	}
	written := 0
	for _, sym := range symbols {
		n, err := deriveSymbol(db, table, sym)
		if err != nil {
			return written, fmt.Errorf("%s: %w", sym, err)
		}
		written += n
	}
	return written, nil
}

type hourRow struct {
	Date   string  `db:"Date"`
	Open   float64 `db:"open"`
	High   float64 `db:"high"`
	Low    float64 `db:"low"`
	Close  float64 `db:"close"`
	Volume int64   `db:"volume"`
}

func deriveSymbol(db *sqlx.DB, table, sym string) (int, error) {
	from := ""
	var last *string
	if err := db.Get(&last, fmt.Sprintf(`SELECT MAX(Date) FROM %s WHERE symbol = ? AND timeframe = '1d'`, table), sym); err != nil {
		return 0, err
	}
	if last != nil && *last != "" {
		if t, err := time.Parse("2006-01-02", (*last)[:10]); err == nil {
			from = t.AddDate(0, 0, -2).Format("2006-01-02") + " 00:00:00"
		}
		// Hourly bars downloaded for an earlier start than the first session leave a gap that
		// reading only from the latest session on would never fill, so read it all.
		var first, firstHour *string
		if err := db.Get(&first, fmt.Sprintf(`SELECT MIN(Date) FROM %s WHERE symbol = ? AND timeframe = '1d'`, table), sym); err != nil {
			return 0, err
		}
		if err := db.Get(&firstHour, fmt.Sprintf(`SELECT MIN(Date) FROM %s WHERE symbol = ? AND timeframe = '1h' AND length(Date) = 19`, table), sym); err != nil {
			return 0, err
		}
		if first != nil && firstHour != nil && len(*first) >= 10 && len(*firstHour) >= 10 {
			f, e1 := time.Parse("2006-01-02", (*first)[:10])
			h, e2 := time.Parse("2006-01-02", (*firstHour)[:10])
			if e1 == nil && e2 == nil && h.AddDate(0, 0, 7).Before(f) {
				from = ""
			}
		}
	}
	var rows []hourRow
	if err := db.Select(&rows, fmt.Sprintf(`SELECT CAST(Date AS TEXT) AS Date, open, high, low, close, volume FROM %s
		WHERE symbol = ? AND timeframe = '1h' AND length(Date) = 19 AND Date >= ? ORDER BY Date`, table), sym, from); err != nil {
		return 0, err
	}
	var out []models.Bar
	var cur *models.Bar
	for _, r := range rows {
		day, ok := Regular(r.Date)
		if !ok || r.Open <= 0 || r.High <= 0 || r.Low <= 0 || r.Close <= 0 {
			continue
		}
		if cur == nil || cur.Date != day {
			out = append(out, models.Bar{Symbol: sym, Date: day, Timeframe: "1d", AssetClass: "equity",
				Open: r.Open, High: r.High, Low: r.Low, Close: r.Close})
			cur = &out[len(out)-1]
		}
		cur.High = max(cur.High, r.High)
		cur.Low = min(cur.Low, r.Low)
		cur.Close, cur.AdjClose = r.Close, r.Close
		cur.Volume += r.Volume
	}
	if len(out) == 0 {
		return 0, nil
	}
	if err := storage.UpsertBars(db, table, out); err != nil {
		return 0, err
	}
	return len(out), nil
}

// Ensure opens the database at path and derives its sessions. A path that does
// not exist is an error, not an empty database.
func Ensure(path string) (int, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return Derive(db, "backtest_start")
}

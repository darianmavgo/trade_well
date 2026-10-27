package scan_market

// Everything in this file builds or reads the MARKET WORKING COPY: the local market file
// (market_history.db) that backtestgosqlite's scan reads. The record is market_bars in Turso; this
// file is rebuilt from it before each scan and only the sessions Turso lacks are written back.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// BuildMarketWorkingCopyFromTurso copies the stored daily bars of symbols from db into the local market file at
// marketDB (table backtest_start), creating the file if needed. It returns the rows copied.
func BuildMarketWorkingCopyFromTurso(ctx context.Context, db *sql.DB, marketDB string, symbols []string, now time.Time) (int, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	marks, args := inList(symbols)
	rows, err := db.QueryContext(ctx,
		`SELECT symbol, date, open, high, low, close, adj_close, volume FROM market_bars
		 WHERE symbol IN (`+marks+`) AND date >= ? ORDER BY symbol, date`, append(args, barsSince(now))...)
	if err != nil {
		return 0, fmt.Errorf("read market_bars: %w", err)
	}
	defer rows.Close()
	var bars []models.Bar
	for rows.Next() {
		b := models.Bar{Timeframe: "1d", AssetClass: "equity"}
		if err := rows.Scan(&b.Symbol, &b.Date, &b.Open, &b.High, &b.Low, &b.Close, &b.AdjClose, &b.Volume); err != nil {
			return 0, err
		}
		bars = append(bars, b)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(bars) == 0 {
		return 0, nil
	}
	local, err := storage.OpenSQLite(marketDB)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	if err := storage.UpsertBars(local, "backtest_start", bars); err != nil {
		return 0, fmt.Errorf("write %s: %w", marketDB, err)
	}
	return len(bars), nil
}

// SaveMarketWorkingCopyToTurso writes the local file's daily bars of symbols to market_bars in db: for each
// symbol, the sessions from the newest one db already has (re-written, in case it was
// provisional) up to today, never older than the window. It returns the rows written.
func SaveMarketWorkingCopyToTurso(ctx context.Context, db *sql.DB, marketDB string, symbols []string, now time.Time) (int, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	marks, args := inList(symbols)
	have := map[string]string{}
	wr, err := db.QueryContext(ctx, `SELECT symbol, MAX(date) FROM market_bars WHERE symbol IN (`+marks+`) GROUP BY symbol`, args...)
	if err != nil {
		return 0, fmt.Errorf("read market_bars watermark: %w", err)
	}
	for wr.Next() {
		var sym, d string
		if err := wr.Scan(&sym, &d); err != nil {
			wr.Close()
			return 0, err
		}
		have[sym] = d
	}
	wr.Close()
	if err := wr.Err(); err != nil {
		return 0, err
	}

	if _, err := os.Stat(marketDB); err != nil {
		return 0, nil // the scan failed before it created the file: nothing to keep
	}
	local, err := storage.OpenSQLiteReadOnly(marketDB)
	if err != nil {
		return 0, err
	}
	defer local.Close()

	since := barsSince(now)
	var vals []any
	n, written := 0, 0
	flush := func() error {
		if n == 0 {
			return nil
		}
		q := `INSERT OR REPLACE INTO market_bars (symbol, date, open, high, low, close, adj_close, volume) VALUES ` +
			strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?),", n), ",")
		_, err := db.ExecContext(ctx, q, vals...)
		written += n
		vals, n = vals[:0], 0
		return err
	}
	for _, a := range args {
		sym := a.(string)
		from := since
		if d := have[sym]; d > from {
			from = d
		}
		var rows []struct {
			Date     string  `db:"d"`
			Open     float64 `db:"open"`
			High     float64 `db:"high"`
			Low      float64 `db:"low"`
			Close    float64 `db:"close"`
			AdjClose float64 `db:"adj"`
			Volume   int64   `db:"volume"`
		}
		if err := local.SelectContext(ctx, &rows,
			`SELECT substr(CAST(Date AS TEXT), 1, 10) AS d, open, high, low, close, COALESCE("Adj Close", close) AS adj, COALESCE(volume, 0) AS volume
			 FROM backtest_start
			 WHERE symbol = ? AND timeframe = '1d' AND length(CAST(Date AS TEXT)) = 10 AND CAST(Date AS TEXT) >= ?
			 ORDER BY Date`, sym, from); err != nil {
			return written, fmt.Errorf("read %s %s: %w", marketDB, sym, err)
		}
		for _, r := range rows {
			vals = append(vals, sym, r.Date, r.Open, r.High, r.Low, r.Close, r.AdjClose, r.Volume)
			n++
			if n == barsBatch {
				if err := flush(); err != nil {
					return written, fmt.Errorf("write market_bars: %w", err)
				}
			}
		}
	}
	if err := flush(); err != nil {
		return written, fmt.Errorf("write market_bars: %w", err)
	}
	return written, nil
}

package modelstate

// Everything in this file builds a WORKING COPY: daily_bars.db on this instance, made from the
// daily bars stored in Turso (daily_bars_live) so backtestgosqlite's SQL stages can read them. Turso
// is the record; the copy is rebuilt whenever it differs and is never read back into Turso.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// BuildDailyBarsWorkingCopyFromTurso makes the daily bars database at barsPath hold the same bars for each symbol as
// daily_bars_live, replacing a symbol's rows only when they differ. It is the working copy the
// model's SQL stages read; Turso is the record. It returns how many symbols it rebuilt.
func BuildDailyBarsWorkingCopyFromTurso(ctx context.Context, db *sql.DB, barsPath string, symbols []string) (int, error) {
	if err := EnsureBarsSchema(db); err != nil {
		return 0, err
	}
	local, err := dailybars.Open(barsPath)
	if err != nil {
		return 0, err
	}
	defer local.Close()
	rebuilt := 0
	for _, sym := range symbols {
		want, err := fingerprintOf(ctx, db, "daily_bars_live", sym)
		if err != nil {
			return rebuilt, fmt.Errorf("modelstate: %s: %w", sym, err)
		}
		if want.n == 0 {
			continue
		}
		have, err := fingerprintOf(ctx, local.DB, "daily_bar", sym)
		if err != nil {
			return rebuilt, fmt.Errorf("modelstate: %s: %w", sym, err)
		}
		if have.equal(want) {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT date, close, adj_close, volume FROM daily_bars_live WHERE symbol = ? ORDER BY date`, sym)
		if err != nil {
			return rebuilt, err
		}
		var bars []models.Bar
		for rows.Next() {
			var b models.Bar
			if err := rows.Scan(&b.Date, &b.Close, &b.AdjClose, &b.Volume); err != nil {
				rows.Close()
				return rebuilt, err
			}
			bars = append(bars, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rebuilt, err
		}
		if err := dailybars.Replace(local, sym, bars); err != nil {
			return rebuilt, fmt.Errorf("modelstate: %s: %w", sym, err)
		}
		rebuilt++
	}
	return rebuilt, nil
}

package modelstate

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// A model is extended from daily bars that go back to its first fitted session (QQQ: 2010), far
// more than the hourly market database keeps. Those bars live in Turso (daily_bars_live), the
// download goes straight there, and the instance's market_history_daily.db is only a working copy built
// from it for the model's SQL stages.

// barsBatch rows per INSERT: six columns x 150 rows stays under SQLite's variable limit, and each
// statement is a network round trip on Turso.
const barsBatch = 150

// EnsureBarsSchema creates the table the daily bars are stored in. Its columns are those of
// daily_bar in market_history_daily.db.
func EnsureBarsSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("modelstate/ensure_bars_schema"))
	if err != nil {
		return fmt.Errorf("modelstate: create daily_bars_live: %w", err)
	}
	return nil
}

// EnsureBars brings daily_bars_live up to the last completed session before now for each symbol,
// downloading only what is missing, straight into Turso. A symbol that is current costs one
// query. One with history downloads from dailybars.OverlapDays before its newest bar and appends
// the new days unless the source restated the days already stored (a dividend or split rescales
// an adjusted history), in which case the whole history is downloaded again and replaces it.
// A symbol with nothing stored is downloaded in full. It returns how many bars it wrote.
func EnsureBars(ctx context.Context, db *sql.DB, symbols []string, now time.Time, log func(string)) (int, error) {
	if err := EnsureBarsSchema(db); err != nil {
		return 0, err
	}
	start, err := time.Parse("2006-01-02", dailybars.DefaultStart)
	if err != nil {
		return 0, err
	}
	completed := calendar.LastCompletedSession(now).Format("2006-01-02")
	written := 0
	for _, sym := range symbols {
		var newest sql.NullString
		if err := db.QueryRowContext(ctx, orchestratorsql.Q("modelstate/ensure_bars"), sym).Scan(&newest); err != nil {
			return written, fmt.Errorf("modelstate: %s: %w", sym, err)
		}
		if newest.Valid && newest.String >= completed {
			continue
		}
		var add []models.Bar
		replaceAll := true
		if newest.Valid {
			from, err := time.Parse("2006-01-02", newest.String)
			if err != nil {
				return written, fmt.Errorf("modelstate: %s: stored date %q: %w", sym, newest.String, err)
			}
			from = from.AddDate(0, 0, -dailybars.OverlapDays)
			held, err := heldTail(ctx, db, sym, from.Format("2006-01-02"))
			if err != nil {
				return written, fmt.Errorf("modelstate: %s: %w", sym, err)
			}
			fetched, err := dailybars.Fetch(ctx, sym, from, now)
			if err != nil {
				return written, fmt.Errorf("modelstate: download the missing %s bars: %w", sym, err)
			}
			var restated bool
			if add, restated = dailybars.Reconcile(held, fetched); restated {
				log(fmt.Sprintf("daily_bars_live: the source restated the %s days already stored (a dividend or split), downloading the whole history again", sym))
			} else {
				replaceAll = false
			}
		}
		if replaceAll {
			if add, err = dailybars.Fetch(ctx, sym, start, now); err != nil {
				return written, fmt.Errorf("modelstate: download the %s history: %w", sym, err)
			}
			if len(add) == 0 {
				return written, fmt.Errorf("modelstate: no completed %s daily bars from %s", sym, dailybars.DefaultStart)
			}
		}
		if len(add) == 0 {
			continue
		}
		if err := storeBars(ctx, db, sym, add, replaceAll); err != nil {
			return written, fmt.Errorf("modelstate: store %s bars: %w", sym, err)
		}
		written += len(add)
	}
	return written, nil
}

func heldTail(ctx context.Context, db *sql.DB, symbol, from string) ([]dailybars.Stored, error) {
	rows, err := db.QueryContext(ctx, orchestratorsql.Q("modelstate/held_tail"), symbol, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dailybars.Stored
	for rows.Next() {
		var s dailybars.Stored
		if err := rows.Scan(&s.Date, &s.AdjClose); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func storeBars(ctx context.Context, db *sql.DB, symbol string, bars []models.Bar, replaceAll bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replaceAll {
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("modelstate/store_bars"), symbol); err != nil {
			return err
		}
	}
	for i := 0; i < len(bars); i += barsBatch {
		end := min(i+barsBatch, len(bars))
		var q strings.Builder
		q.WriteString(orchestratorsql.Q("modelstate/store_bars_2"))
		args := make([]any, 0, (end-i)*6)
		for j, b := range bars[i:end] {
			if j > 0 {
				q.WriteByte(',')
			}
			q.WriteString("(?,?,?,?,?,?)")
			args = append(args, symbol, b.Date, b.Close, b.AdjClose, b.Volume, dailybars.Source)
		}
		if _, err := tx.ExecContext(ctx, q.String(), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// fingerprint summarizes one symbol's held bars so a working copy can be compared with Turso
// without reading every row: the count, the newest date and the sum of the adjusted closes (a
// restated history changes the sum).
type fingerprint struct {
	n      int
	newest string
	adjSum float64
}

func (a fingerprint) equal(b fingerprint) bool {
	return a.n == b.n && a.newest == b.newest && math.Abs(a.adjSum-b.adjSum) <= 1e-9*math.Max(1, math.Abs(a.adjSum))
}

func fingerprintOf(ctx context.Context, db *sql.DB, table, symbol string) (fingerprint, error) {
	var f fingerprint
	var newest sql.NullString
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(date), SUM(adj_close) FROM `+table+` WHERE symbol = ?`, symbol).Scan(&f.n, &newest, &sum)
	f.newest, f.adjSum = newest.String, sum.Float64
	return f, err
}

// Package dailybars keeps long-history daily bars for the few symbols a model needs more years
// of than the hourly market database holds (the HMM on QQQ needs 2010 onward; hourly bars go
// back about ten years and cost a decade of downloads). Bars come from the Yahoo source and
// land in their own small database, daily_bars.db; the hourly database is never written.
//
// The closes are dividend- and split-adjusted by the source, and an adjusted history is
// rescaled whenever a dividend is paid. So a run downloads only what is missing, but first
// checks the days it already holds against the source (Reconcile): if the source now gives them
// different adjusted closes, the history was restated and the whole of it is downloaded again.
package dailybars

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// Source is recorded on every row so a mixed history is never mistaken for one provider.
const Source = "yahoo"

// DefaultStart is where the history begins: after the 2008-09 crash and the QQQ ticker change,
// with 16 years of sessions.
const DefaultStart = "2010-01-01"

const schema = `CREATE TABLE IF NOT EXISTS daily_bar (
	symbol    TEXT NOT NULL,
	date      TEXT NOT NULL,
	close     REAL NOT NULL,
	adj_close REAL NOT NULL,
	volume    INTEGER NOT NULL,
	source    TEXT NOT NULL,
	PRIMARY KEY (symbol, date)
) WITHOUT ROWID`

// Config holds every setting of a run.
type Config struct {
	DB      string   // daily bars database (default appenv.DailyBarsDB())
	Symbols []string // default QQQ
	Start   string   // YYYY-MM-DD (default DefaultStart)
	DryRun  bool     // fetch and report, write nothing
	Full    bool     // download the whole history again even when it is current
	Now     time.Time
}

// Result reports one symbol.
type Result struct {
	Symbol string
	Bars   int
	First  string
	Last   string
}

// OverlapDays is how far back an incremental download starts, so it re-reads days it already
// holds and can tell whether the source restated them.
const OverlapDays = 14

// Stored is a held bar's date and adjusted close, for Reconcile.
type Stored struct {
	Date     string
	AdjClose float64
}

// restatedTolerance is the relative difference in an adjusted close that counts as the source
// having rescaled the history. Rounding in the source stays far below it; a dividend does not.
const restatedTolerance = 1e-6

// Reconcile compares fetched bars (a download that starts inside the days already held) with
// the held tail. It returns the fetched bars newer than the newest held one, and restated=true
// when the whole history must be downloaded again instead: the source gave a different
// adjusted close for a day both have, or the two share no day at all so nothing can be checked.
// With nothing held, every fetched bar is new.
func Reconcile(held []Stored, fetched []models.Bar) (add []models.Bar, restated bool) {
	if len(held) == 0 {
		return fetched, false
	}
	newest := ""
	byDate := make(map[string]float64, len(held))
	for _, h := range held {
		byDate[h.Date] = h.AdjClose
		if h.Date > newest {
			newest = h.Date
		}
	}
	compared := 0
	for _, b := range fetched {
		if old, ok := byDate[b.Date]; ok {
			compared++
			if old <= 0 || math.Abs(b.AdjClose-old)/old > restatedTolerance {
				return nil, true
			}
		}
		if b.Date > newest {
			add = append(add, b)
		}
	}
	if compared == 0 {
		return nil, true
	}
	return add, false
}

// Fetch downloads symbol's completed daily bars from start through the last completed session
// before now, one per day in date order.
func Fetch(ctx context.Context, symbol string, start, now time.Time) ([]models.Bar, error) {
	src := datasource.NewYahooDataSource(nil)
	bars, err := src.Fetch(ctx, datasource.FetchRequest{Symbol: symbol, StartDate: start, EndDate: now.Add(24 * time.Hour), Timeframe: "1d", AssetClass: "equity"})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", symbol, err)
	}
	return completed(bars, calendar.LastCompletedSession(now).Format("2006-01-02")), nil
}

// LastDate is the newest date held for symbol in the daily bars database at path, and whether
// there is one. The file is opened read-only and never created.
func LastDate(path, symbol string) (string, bool) {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var last sql.NullString
	if err := db.Get(&last, `SELECT MAX(date) FROM daily_bar WHERE symbol = ?`, symbol); err != nil || !last.Valid {
		return "", false
	}
	return last.String, true
}

func heldTail(db *sqlx.DB, symbol string, from string) ([]Stored, error) {
	var out []Stored
	err := db.Select(&out, `SELECT date AS "date", adj_close AS "adjclose" FROM daily_bar WHERE symbol = ? AND date >= ? ORDER BY date`, symbol, from)
	return out, err
}

// Run brings each symbol's stored daily bars up to the last completed session. A current symbol
// is not downloaded at all. One with history downloads only from OverlapDays before its newest
// bar, and appends what is new unless the source restated the days it already holds, in which
// case (and for a symbol with nothing stored, or with Full) the whole history from Start is
// downloaded and replaces the stored one. A symbol that fails leaves its stored rows untouched
// and the error is returned after the others have been tried.
func Run(ctx context.Context, cfg Config, out io.Writer) ([]Result, error) {
	if cfg.DB == "" {
		cfg.DB = appenv.DailyBarsDB()
	}
	if len(cfg.Symbols) == 0 {
		cfg.Symbols = []string{"QQQ"}
	}
	if cfg.Start == "" {
		cfg.Start = DefaultStart
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	start, err := time.Parse("2006-01-02", cfg.Start)
	if err != nil {
		return nil, fmt.Errorf("daily_bars: bad start %q: %w", cfg.Start, err)
	}
	last := calendar.LastCompletedSession(cfg.Now).Format("2006-01-02")

	var db *sqlx.DB
	if !cfg.DryRun {
		if db, err = Open(cfg.DB); err != nil {
			return nil, err
		}
		defer db.Close()
	}

	var results []Result
	var errs []error
	for _, sym := range cfg.Symbols {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			continue
		}
		var bars []models.Bar
		appended := false
		if db != nil && !cfg.Full {
			var newest sql.NullString
			if err := db.Get(&newest, `SELECT MAX(date) FROM daily_bar WHERE symbol = ?`, sym); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sym, err))
				continue
			}
			if newest.Valid && newest.String >= last {
				fmt.Fprintf(out, "[daily_bars] %s: current through %s, nothing to download\n", sym, newest.String)
				results = append(results, Result{Symbol: sym, Last: newest.String})
				continue
			}
			if newest.Valid {
				from, perr := time.Parse("2006-01-02", newest.String)
				if perr != nil {
					errs = append(errs, fmt.Errorf("%s: stored date %q: %w", sym, newest.String, perr))
					continue
				}
				from = from.AddDate(0, 0, -OverlapDays)
				held, herr := heldTail(db, sym, from.Format("2006-01-02"))
				if herr != nil {
					errs = append(errs, fmt.Errorf("%s: %w", sym, herr))
					continue
				}
				fetched, ferr := Fetch(ctx, sym, from, cfg.Now)
				if ferr != nil {
					errs = append(errs, ferr)
					continue
				}
				add, restated := Reconcile(held, fetched)
				if !restated {
					bars, appended = add, true
				} else {
					fmt.Fprintf(out, "[daily_bars] %s: the source restated the days already held (a dividend or split), downloading the whole history again\n", sym)
				}
			}
		}
		if !appended {
			if bars, err = Fetch(ctx, sym, start, cfg.Now); err != nil {
				errs = append(errs, err)
				continue
			}
			if len(bars) == 0 {
				errs = append(errs, fmt.Errorf("%s: no completed daily bars from %s", sym, cfg.Start))
				continue
			}
		}
		res := Result{Symbol: sym, Bars: len(bars)}
		if len(bars) > 0 {
			res.First, res.Last = bars[0].Date, bars[len(bars)-1].Date
		}
		if db != nil && len(bars) > 0 {
			var werr error
			if appended {
				werr = Append(db, sym, bars)
			} else {
				werr = Replace(db, sym, bars)
			}
			if werr != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sym, werr))
				continue
			}
		}
		verb := "stored"
		switch {
		case cfg.DryRun:
			verb = "would store"
		case appended:
			verb = "appended"
		}
		fmt.Fprintf(out, "[daily_bars] %s: %s %d bars %s..%s\n", sym, verb, res.Bars, res.First, res.Last)
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}

// completed keeps the bars on or before the last completed session, in date order, one per day.
// A bar for today is still forming and would change by tomorrow.
func completed(bars []models.Bar, last string) []models.Bar {
	seen := map[string]bool{}
	var out []models.Bar
	for _, b := range bars {
		d := b.Date
		if len(d) > 10 {
			d = d[:10]
		}
		if d > last || seen[d] || b.Close <= 0 || b.AdjClose <= 0 {
			continue
		}
		seen[d] = true
		b.Date = d
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// Open opens (creating if needed) the daily bars database.
func Open(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("daily_bars: create schema: %w", err)
	}
	return db, nil
}

// Replace swaps one symbol's stored history for bars, in one transaction.
func Replace(db *sqlx.DB, symbol string, bars []models.Bar) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM daily_bar WHERE symbol = ?`, symbol); err != nil {
		return err
	}
	stmt, err := tx.Preparex(`INSERT INTO daily_bar (symbol, date, close, adj_close, volume, source) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, b := range bars {
		if _, err := stmt.Exec(symbol, b.Date, b.Close, b.AdjClose, b.Volume, Source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Append adds bars to one symbol's stored history, replacing any day it already holds.
func Append(db *sqlx.DB, symbol string, bars []models.Bar) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Preparex(`INSERT OR REPLACE INTO daily_bar (symbol, date, close, adj_close, volume, source) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, b := range bars {
		if _, err := stmt.Exec(symbol, b.Date, b.Close, b.AdjClose, b.Volume, Source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Main is the daily_bars command.
func Main() { os.Exit(RunCLI(os.Args[1:], os.Stdout, os.Stderr)) }

// RunCLI is Main without os.Args/os.Exit. It returns the exit code.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daily_bars", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `daily_bars: download long-history daily bars (default QQQ from 2010) into daily_bars.db.

Brings each named symbol's stored history up to the last completed session, downloading only
what is missing; it downloads the whole history again when the source restated the days already
held (a dividend or split) or with -full. Writes only daily_bars.db, never the hourly market
database. -dry-run downloads and reports but writes nothing.`)
		fs.PrintDefaults()
	}
	db := fs.String("db", appenv.DailyBarsDB(), "daily bars database")
	symbols := fs.String("symbols", "QQQ", "comma-separated symbols")
	start := fs.String("start", DefaultStart, "first date, YYYY-MM-DD")
	dry := fs.Bool("dry-run", false, "download and report, write nothing")
	full := fs.Bool("full", false, "download the whole history again even when it is current")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	syms := append(strings.Split(*symbols, ","), fs.Args()...)
	if _, err := Run(context.Background(), Config{DB: *db, Symbols: syms, Start: *start, DryRun: *dry, Full: *full}, stdout); err != nil {
		fmt.Fprintln(stderr, "daily_bars:", err)
		return 1
	}
	return 0
}

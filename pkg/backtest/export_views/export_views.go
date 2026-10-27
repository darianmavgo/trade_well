// Package export_views writes the indicator calculations (moving averages,
// streaks, decision-tree features, average volume, split jumps) as SQL views
// over the market database's backtest_start table, so any SQLite client can
// query them live. The view definitions are sql/views/*.sql.
package export_views

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// Options says where the views come from and go to.
type Options struct {
	MarketDB string   // source market database; copied, never written
	SQLOut   string   // file receiving the CREATE VIEW script; empty skips it
	DBOut    string   // copy of MarketDB with the views created in it; empty skips it
	Symbols  []string // symbols whose bars go in DBOut; default the strategy's own, or every symbol with no -strategy
	DryRun   bool     // list the views and write nothing
	Strategy string   // strategy id: export that strategy's own pipeline as views; empty exports the shared indicator views
}

var viewName = regexp.MustCompile(`(?im)^\s*CREATE\s+VIEW\s+(\w+)`)

// Script returns every view file in name order, joined into one SQL script,
// and the names of the views it creates.
func Script() (script string, views []string, err error) {
	files, err := fs.Glob(sqlfiles.FS, "views/*.sql")
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	for _, f := range files {
		body, err := fs.ReadFile(sqlfiles.FS, f)
		if err != nil {
			return "", nil, err
		}
		for _, m := range viewName.FindAllStringSubmatch(string(body), -1) {
			views = append(views, m[1])
		}
		fmt.Fprintf(&b, "-- %s\n%s\n", filepath.Base(f), strings.TrimSpace(string(body)))
	}
	return b.String(), views, nil
}

// Run exports the views as the options ask. The market database is only read.
func Run(ctx context.Context, o Options) ([]string, error) {
	script, views, err := Script()
	var symbols []string
	if o.Strategy != "" {
		script, views, symbols, err = StrategyScript(o.Strategy)
	}
	if err != nil {
		return nil, err
	}
	if len(o.Symbols) > 0 {
		symbols = o.Symbols
	}
	if o.DryRun {
		return views, nil
	}
	if o.SQLOut != "" {
		if err := os.MkdirAll(filepath.Dir(o.SQLOut), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(o.SQLOut, []byte(script), 0o644); err != nil {
			return nil, err
		}
	}
	if o.DBOut == "" {
		return views, nil
	}
	if same(o.MarketDB, o.DBOut) {
		return nil, fmt.Errorf("db-out %s is the market database; views go in a new file", o.DBOut)
	}
	if err := writeSubset(ctx, o.MarketDB, o.DBOut, symbols); err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(o.DBOut)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, script); err != nil {
		return nil, fmt.Errorf("create views in %s: %w", o.DBOut, err)
	}
	return views, nil
}

// writeSubset creates dst holding backtest_start (same schema and indexes as src) with only the daily
// session rows the views read, for the given symbols, or for every symbol when none are given. The
// hourly bars are left out: no view reads them. dst is replaced; src is only read.
func writeSubset(ctx context.Context, src, dst string, symbols []string) error {
	for _, sym := range symbols {
		if _, ok := strategy.StreakSymbol(sym); !ok {
			return fmt.Errorf("symbol %q is not a valid ticker", sym)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dst + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	db, err := storage.OpenSQLite(dst)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // ATTACH belongs to one connection
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "ATTACH DATABASE ? AS mkt", abs); err != nil {
		return fmt.Errorf("attach %s: %w", src, err)
	}
	var ddl []string
	if err := db.SelectContext(ctx, &ddl, `SELECT sql FROM mkt.sqlite_master
		WHERE tbl_name = 'backtest_start' AND type IN ('table', 'index') AND sql IS NOT NULL
		ORDER BY type DESC`); err != nil {
		return err
	}
	if len(ddl) == 0 {
		return fmt.Errorf("%s has no backtest_start table", src)
	}
	for _, q := range ddl {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("copy schema: %w", err)
		}
	}
	q := "INSERT INTO main.backtest_start SELECT * FROM mkt.backtest_start WHERE length(Date) = 10"
	var args []any
	if len(symbols) > 0 {
		q += " AND symbol IN (?" + strings.Repeat(",?", len(symbols)-1) + ")"
		for _, sym := range symbols {
			args = append(args, strings.ToUpper(strings.TrimSpace(sym)))
		}
	}
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("copy bars: %w", err)
	}
	_, err = db.ExecContext(ctx, "DETACH DATABASE mkt")
	return err
}

// StrategyScript returns the views for one strategy id: its signal pipeline with the strategy's own
// parameters filled in, and the symbols whose bars it reads. Only a strategy whose pipeline reads the
// market table alone can be exported.
func StrategyScript(id string) (script string, views, symbols []string, err error) {
	stratreg.RegisterFamilies()
	st, ok := strategy.Get(id)
	if !ok {
		return "", nil, nil, fmt.Errorf("unknown strategy %q (see backtest -strategylist)", id)
	}
	var dir string
	switch p := st.(type) {
	case interface{ SQLDir() string }:
		dir = p.SQLDir()
	case interface{ PipelineDir() string }:
		dir = p.PipelineDir()
	default:
		return "", nil, nil, fmt.Errorf("strategy %s does not expose a SQL pipeline directory (tree and markov strategies also read a trained model, which a view cannot)", id)
	}
	script, views, err = strategy.PipelineViews(st.ID(), dir, st.DefaultConfig())
	if err != nil {
		return "", nil, nil, fmt.Errorf("strategy %s: %w", st.ID(), err)
	}
	if rs, ok := st.(strategy.RequiredSymbolsProvider); ok {
		symbols = rs.RequiredSymbols()
	}
	if len(symbols) == 0 {
		return "", nil, nil, fmt.Errorf("strategy %s does not say which symbols it reads; pass -symbols", st.ID())
	}
	return script, views, symbols, nil
}

func same(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return aa == bb
}

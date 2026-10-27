package market_history

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/barcoverage"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// UpdateConfig is the settings of Update. Config.Symbols is ignored: the
// symbols come from the strategies.
type UpdateConfig struct {
	Config
	DryRun bool // report what is missing, download nothing
}

// DefaultUpdateConfig is DefaultConfig with the update defaults: Alpaca hourly
// bars from the standard backtest start, every symbol (no -limit).
func DefaultUpdateConfig() UpdateConfig {
	c := DefaultConfig()
	c.Source, c.Timeframe, c.Start, c.Limit = "alpaca", "1h", "2021-01-01", 0
	return UpdateConfig{Config: c}
}

// StrategySymbols is every symbol the strategies (ids or a+b+c stacks) need,
// sorted: candidates, regime symbol and benchmark.
func StrategySymbols(ids []string) ([]string, error) {
	set := map[string]bool{}
	for _, entry := range ids {
		for _, id := range strategy.ParseStack(strings.TrimSpace(entry)) {
			if id == "" {
				continue
			}
			s, ok := strategy.Get(id)
			if !ok {
				return nil, fmt.Errorf("unknown strategy %q (see ./bin/strategy)", id)
			}
			for _, sym := range runner.RequiredSymbolsFor([]strategy.Strategy{s}, "") {
				set[sym] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for sym := range set {
		out = append(out, sym)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("those strategies name no symbols (a universe-wide strategy uses every symbol)")
	}
	return out, nil
}

// Update checks which of symbols have sessions with no bars in the target
// database, between cfg.Start and cfg.End, and downloads only those. A symbol
// with a gap is fetched again over the whole window (Alpaca returns up to
// 10,000 bars a request and pages itself), replacing bars already stored at
// the same time, because a normal run fills only the two ends of a symbol's
// range and never a gap inside it. Symbols with no gap are not requested.
// It returns the symbols that were downloaded.
func Update(ctx context.Context, cfg UpdateConfig, symbols []string) ([]string, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	if cfg.DB == "" {
		return nil, fmt.Errorf("market_history: DB is required")
	}
	if len(symbols) == 0 {
		return nil, fmt.Errorf("market_history update: no symbols")
	}
	if cfg.Timeframe == "" {
		cfg.Timeframe = "1h"
	}
	if cfg.Start == "" {
		cfg.Start = "2021-01-01"
	}
	if _, err := sessions.Ensure(appenv.BarDB(cfg.DB, cfg.Timeframe)); err != nil {
		return nil, fmt.Errorf("derive sessions: %w", err)
	}
	copts := barcoverage.CoverageOptions{
		Symbols: symbols, DB: appenv.BarDB(cfg.DB, cfg.Timeframe), Start: cfg.Start, End: cfg.End,
	}
	before, err := barcoverage.Coverage(copts)
	if err != nil {
		return nil, err
	}
	var need []string
	for _, c := range before {
		if c.Expected > 0 && c.Missing > 0 {
			need = append(need, c.Symbol)
		}
	}
	complete := len(before) - len(need)
	fmt.Fprintf(out, "%d of %d symbols are missing sessions of %s bars since %s; %d are complete.\n",
		len(need), len(before), cfg.Timeframe, cfg.Start, complete)
	for _, c := range before {
		switch {
		case c.Expected == 0:
			fmt.Fprintf(out, "  %-6s skipped: %s\n", c.Symbol, c.Note)
		case c.Missing > 0:
			fmt.Fprintf(out, "  %-6s %d of %d sessions missing (longest gap %d)\n", c.Symbol, c.Missing, c.Expected, c.LongestGap)
		}
	}
	if len(need) == 0 || cfg.DryRun {
		return nil, nil
	}

	run := cfg.Config
	run.Symbols = strings.Join(need, ",")
	run.Limit = 0
	run.Force = true
	if _, err := Run(ctx, run); err != nil {
		return need, err
	}
	after, err := barcoverage.Coverage(copts)
	if err != nil {
		return need, err
	}
	fmt.Fprintf(out, "\nAfter the download:\n")
	barcoverage.WriteCoverage(out, copts, onlyIncomplete(after))
	return need, nil
}

func onlyIncomplete(rows []barcoverage.SymbolCoverage) []barcoverage.SymbolCoverage {
	var out []barcoverage.SymbolCoverage
	for _, c := range rows {
		if c.Missing > 0 || c.Expected == 0 {
			out = append(out, c)
		}
	}
	return out
}

// updateMain is `market_history update -strategy <id>`.
func updateMain() {
	d := DefaultUpdateConfig()
	cfg := d
	bindFlags(&cfg.Config, d.Config)
	strategies := flag.String("strategy", "", "required: strategy ids and a+b+c stacks, comma-separated; update covers the symbols they need")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "report the symbols with missing sessions, download nothing")
	flag.Parse()
	if strings.TrimSpace(*strategies) == "" {
		log.Fatal("update needs -strategy <id>: it says which symbols to keep complete")
	}
	if err := appenv.RequireHourly(cfg.DB); err != nil {
		log.Fatal(err)
	}
	strategy.AutoRegisterSQLStrategies(appenv.Folder(), cfg.DB)
	stratreg.RegisterFamilies()
	symbols, err := StrategySymbols(strings.Split(*strategies, ","))
	if err != nil {
		log.Fatal(err)
	}
	cfg.Out = os.Stdout
	resolveKeys(&cfg.Config)
	if _, err := Update(context.Background(), cfg, symbols); err != nil {
		log.Fatal(err)
	}
}

// sessionsMain is `market_history sessions`: rebuild the daily session rows
// from the hourly bars of the database.
func sessionsMain() {
	db := flag.String("db", appenv.MarketDB(), "hourly market database")
	flag.Parse()
	if err := appenv.RequireHourly(*db); err != nil {
		log.Fatal(err)
	}
	n, err := sessions.Ensure(*db)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d session bars in %s\n", n, *db)
}

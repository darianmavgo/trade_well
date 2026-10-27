package walk_forward

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	_ "modernc.org/sqlite"
)

// Config is the CLI surface of a walk-forward run.
type Config struct {
	Strategy string
	MarketDB string
	Table    string
	DB       string
	// KeepGoing skips a strategy that cannot be walked (short history, no
	// bars) and continues, instead of aborting a long list.
	KeepGoing bool
	// RunID is the run folder holding walk_forward.db when DB is empty; 0 starts a new run.
	RunID int
	Options
}

// Run loads bars, walks each strategy, and writes fold rows plus the summary.
func Run(ctx context.Context, out io.Writer, conf Config) error {
	stratreg.RegisterAll(appenv.Folder(), conf.MarketDB)
	ids := splitIDs(conf.Strategy)
	if len(ids) == 0 {
		return fmt.Errorf("walk_forward: -strategy is required")
	}
	if strings.TrimSpace(conf.DB) == "" {
		path, id, err := storage.RunFile(appenv.Reports(), conf.RunID, true, "walk_forward.db")
		if err != nil {
			return fmt.Errorf("walk_forward: %w", err)
		}
		conf.DB = path
		fmt.Fprintf(out, "run %d: %s\n", id, conf.DB)
	}
	market, err := storage.OpenSQLite(conf.MarketDB)
	if err != nil {
		return err
	}
	defer market.Close()
	outDB, err := sql.Open("sqlite", conf.DB)
	if err != nil {
		return err
	}
	defer outDB.Close()
	if err := EnsureSchema(outDB); err != nil {
		return err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		strat, ok := strategy.Get(id)
		if !ok {
			return fmt.Errorf("walk_forward: strategy %q is not registered", id)
		}
		strat.SetDatabases(conf.MarketDB, conf.DB)
		var symbols []string
		if p, ok := strat.(strategy.RequiredSymbolsProvider); ok {
			symbols = p.RequiredSymbols()
		}
		bars, _, err := storage.FetchBars(market, conf.Table, symbols, "", "")
		if err != nil {
			return fmt.Errorf("%s: %w", strat.ID(), err)
		}
		opt := conf.Options
		if opt.Hourly == nil {
			if opt.Hourly, err = runner.LoadFillBars(conf.MarketDB, symbols); err != nil {
				fmt.Fprintf(out, "no hourly bars for %s (%v): sessions are played as daily bars\n", strat.ID(), err)
				opt.Hourly = nil
			}
		}
		rows, err := RunStrategy(ctx, strat, bars, nil, opt)
		if err != nil {
			if conf.KeepGoing {
				fmt.Fprintf(out, "SKIP %s: %v\n", strat.ID(), err)
				continue
			}
			return err
		}
		if err := Save(outDB, strat.ID(), rows); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s  folds=%d  trials=%d  db=%s\n", strat.ID(), len(rows), TrialCount(strat), conf.DB)
		fmt.Fprint(out, FormatRows(rows))
	}
	fmt.Fprintln(out, "summary table: walk_forward_summary")
	return nil
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

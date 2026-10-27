package check_overfit

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	_ "modernc.org/sqlite"
)

// Config is the CLI surface.
type Config struct {
	DB       string
	RunID    int    // run folder holding walk_forward.db when DB is empty; 0 = the latest run
	Strategy string // comma-separated ids to show; empty or "all" shows every strategy in the file
	Gates    Gates
}

// Report prints the verdict of every strategy in the walk-forward database: the
// file named by conf.DB, otherwise walk_forward.db in the run folder conf.RunID
// (the latest run when 0). It is what validate runs after the folds.
func Report(ctx context.Context, out io.Writer, conf Config) error {
	if conf.DB == "" {
		path, _, err := storage.RunFile(appenv.Reports(), conf.RunID, false, "walk_forward.db")
		if err != nil {
			return fmt.Errorf("check_overfit: %w (run validate first)", err)
		}
		conf.DB = path
	}
	db, err := sql.Open("sqlite", conf.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := Run(ctx, db, conf.Gates)
	if err != nil {
		return err
	}
	rows = Only(rows, conf.Strategy)
	if len(rows) == 0 {
		return fmt.Errorf("check_overfit: walk_forward_summary has no strategies")
	}
	fmt.Fprintf(out, "db: %s\n", conf.DB)
	Write(out, rows)
	fmt.Fprintln(out, "verdicts: HOLDS, DECAYS, CURVE_FIT, INSUFFICIENT")
	return nil
}

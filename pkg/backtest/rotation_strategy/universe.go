package rotation_strategy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// CandidatesListID is the symbol_lists id the candidate symbols are stored under. A rotation
// row names it in its symbols column, so every row shares one list.
const CandidatesListID = "rotation-candidates"

// UniverseConfig is one run of the rotation universe job.
type UniverseConfig struct {
	MarketDB string    // market database holding the daily bars (read only)
	RefDB    string    // reference database whose symbol_lists gets the candidates
	ListID   string    // symbol_lists id to write, CandidatesListID when empty
	CalcDB   string    // scratch database for the slice tables, reports/calc/rotation_universe.db when empty
	KeepCalc bool      // keep the scratch database for inspection
	DryRun   bool      // compute and report, write nothing to RefDB
	Out      io.Writer // progress lines, nil for none
}

// YearReturn is the calendar year return of one curve, in percent.
type YearReturn struct {
	Curve    string  `db:"curve"`
	Year     string  `db:"yr"`
	Sessions int     `db:"n"`
	Percent  float64 `db:"total_pct"`
}

// UniverseResult is what the job found.
type UniverseResult struct {
	Candidates int          // symbols that were ever among the 1000 most liquid at a close
	ListID     string       // symbol_lists id the candidates are stored under
	Written    bool         // false on a dry run
	Yearly     []YearReturn // calendar year return of each equal weight top-K curve
}

// Universe runs the rotation_universe stage (sql/stages/rotation_universe): the point in time
// liquid universe, the candidate symbols and the equal weight top-K curves, each a slice table in
// a fresh scratch database. It stores the candidates as one symbol list in the reference database,
// which the rotation rows name in their symbols column. The scratch database is removed when the
// job ends unless KeepCalc is set.
func Universe(ctx context.Context, cfg UniverseConfig) (UniverseResult, error) {
	res := UniverseResult{ListID: cfg.ListID}
	if res.ListID == "" {
		res.ListID = CandidatesListID
	}
	if cfg.MarketDB == "" {
		cfg.MarketDB = appenv.MarketDB()
	}
	if _, err := os.Stat(cfg.MarketDB); err != nil {
		return res, fmt.Errorf("[rotation_universe] market database: %w", err)
	}
	if cfg.CalcDB == "" {
		cfg.CalcDB = filepath.Join(appenv.Reports(), "calc", "rotation_universe.db")
	}
	if err := removeDB(cfg.CalcDB); err != nil {
		return res, err
	}
	if !cfg.KeepCalc {
		defer removeDB(cfg.CalcDB)
	}
	logf := func(format string, a ...any) {
		if cfg.Out != nil {
			fmt.Fprintf(cfg.Out, "[rotation_universe] "+format+"\n", a...)
		}
	}

	calc, err := storage.OpenSQLite(cfg.CalcDB)
	if err != nil {
		return res, err
	}
	defer calc.Close()
	calc.SetMaxOpenConns(1) // the ATTACH must be seen by the statements that follow
	if _, err := calc.ExecContext(ctx, `ATTACH DATABASE ? AS m`, cfg.MarketDB); err != nil {
		return res, fmt.Errorf("[rotation_universe] attach %s: %w", cfg.MarketDB, err)
	}
	logf("ranking %s into %s", cfg.MarketDB, cfg.CalcDB)
	if err := storage.RunStage(calc, "rotation_universe", nil); err != nil {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	var list string
	if err := calc.GetContext(ctx, &list, `SELECT COALESCE(group_concat(symbol, ','), '') FROM (SELECT symbol FROM rotation_candidates ORDER BY symbol)`); err != nil {
		return res, err
	}
	if list != "" {
		res.Candidates = strings.Count(list, ",") + 1
	}
	if err := calc.SelectContext(ctx, &res.Yearly, `SELECT curve, yr, n, total_pct FROM clone_yearly ORDER BY curve, yr`); err != nil {
		return res, err
	}
	logf("%d candidates", res.Candidates)
	if res.Candidates == 0 {
		return res, fmt.Errorf("[rotation_universe] no candidates: %s has no daily bars with 127 sessions of history", cfg.MarketDB)
	}
	if cfg.DryRun {
		logf("dry run: %s not written", res.ListID)
		return res, nil
	}
	if err := writeList(ctx, cfg.RefDB, res.ListID, list); err != nil {
		return res, err
	}
	res.Written = true
	logf("wrote symbol list %s to %s", res.ListID, cfg.RefDB)
	return res, nil
}

// writeList stores list under id in the reference database at path, replacing an earlier list.
func writeList(ctx context.Context, path, id, list string) error {
	if path == "" {
		path = appenv.RefDB()
	}
	db, err := refdb.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	return execList(ctx, db, id, list)
}

func execList(ctx context.Context, db *sqlx.DB, id, list string) error {
	_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list) VALUES (?, ?)`, id, list)
	return err
}

// removeDB deletes a SQLite file and its sidecars, so a run never reads a table an earlier run left.
func removeDB(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old scratch database %s: %w", path+suffix, err)
		}
	}
	return nil
}

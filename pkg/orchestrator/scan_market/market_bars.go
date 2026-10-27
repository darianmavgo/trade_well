package scan_market

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/modelstate"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// The scan reads and extends a local market file (backtest_start). On Turso the instance's file
// is empty after every cold start, so the daily bars the scan needs are kept in market_bars in
// the orchestrator database: BuildMarketWorkingCopyFromTurso copies them into the local file before the scan, and
// SaveMarketWorkingCopyToTurso writes back only the sessions Turso does not have yet. Only the symbols the
// scanned strategies require are ever copied, and only the last barsWindowYears years.

const (
	barsWindowYears = 2
	// barsBatch rows per INSERT: each statement is a network round trip on Turso, and 8 columns
	// x 200 rows stays under SQLite's variable limit.
	barsBatch = 200
)

// OnTurso reports whether the orchestrator database is Turso, the only case the bars are kept
// there. Under `go test` it is always false.
func OnTurso() bool {
	_, remote, _ := dbutil.Turso()
	return remote
}

// EnsureBarsSchema creates market_bars: one row per symbol per completed daily session.
func EnsureBarsSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("scan_market/ensure_bars_schema"))
	return err
}

func barsSince(now time.Time) string {
	return now.AddDate(-barsWindowYears, 0, 0).Format("2006-01-02")
}

func inList(symbols []string) (string, []any) {
	args := make([]any, len(symbols))
	for i, s := range symbols {
		args[i] = strings.ToUpper(strings.TrimSpace(s))
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(symbols)), ","), args
}

// bars wraps a RunLiveScan: on Turso it first loads the stored bars of symbols into the local
// market file and afterwards stores what the scan added. Both are caches of the data source, so
// a failure is reported in details and never fails the scan.
func syncBars(ctx context.Context, db *sql.DB, marketDB string, symbols []string, details *[]string) (persist func()) {
	if !OnTurso() {
		return func() {}
	}
	now := time.Now()
	if n, err := BuildMarketWorkingCopyFromTurso(ctx, db, marketDB, symbols, now); err != nil {
		*details = append(*details, fmt.Sprintf("market_bars: load from Turso failed (the scan downloads instead): %v", err))
	} else {
		*details = append(*details, fmt.Sprintf("market_bars: loaded %d stored bar(s) for %d symbol(s) from Turso", n, len(symbols)))
	}
	persistModel := syncModel(ctx, db, details)
	return func() {
		if n, err := SaveMarketWorkingCopyToTurso(ctx, db, marketDB, symbols, time.Now()); err != nil {
			*details = append(*details, fmt.Sprintf("market_bars: save to Turso failed: %v", err))
		} else {
			*details = append(*details, fmt.Sprintf("market_bars: saved %d new/updated bar(s) to Turso", n))
		}
		persistModel()
	}
}

// syncModel does for the trained models what syncBars does for bars. The hmm and markov
// strategies extend a saved model file with one prediction per new session, and that file is
// rebuilt from the bundle on every cold start. So it loads the predictions stored in Turso into
// this instance's model files, which lets a strategy's Prepare see which sessions are still
// unpredicted, and afterwards stores the predictions Prepare added. Like the bars it is a cache
// of work that can be redone, so a failure is reported and Prepare predicts the missing
// sessions itself.
func syncModel(ctx context.Context, db *sql.DB, details *[]string) (persist func()) {
	note := func(m string) { *details = append(*details, m) }
	var after []func()

	if path := appenv.HMMDB(); modelFileExists(path) {
		// The hmm model is extended from daily bars that go back to its first fitted session.
		// They are kept in Turso and only the missing days are downloaded, straight into it; the
		// instance's daily_bars.db is a working copy built from them.
		if symbols, err := modelstate.ReadHMMModelAssetSymbols(path); err != nil {
			note(fmt.Sprintf("daily_bars_live: cannot read the model's symbols: %v", err))
		} else {
			if n, err := modelstate.EnsureBars(ctx, db, symbols, time.Now(), note); err != nil {
				note(fmt.Sprintf("daily_bars_live: bringing the stored bars up to date failed: %v", err))
			} else {
				note(fmt.Sprintf("daily_bars_live: downloaded %d missing bar(s) into Turso", n))
			}
			if n, err := modelstate.BuildDailyBarsWorkingCopyFromTurso(ctx, db, appenv.DailyBarsDB(), symbols); err != nil {
				note(fmt.Sprintf("daily_bars_live: loading the working copy from Turso failed: %v", err))
			} else {
				note(fmt.Sprintf("daily_bars_live: rebuilt the working copy of %d symbol(s) from Turso", n))
			}
		}
		if n, err := modelstate.HydrateHMMModelAssetFromTurso(ctx, db, path); err != nil {
			note(fmt.Sprintf("hmm_prediction: load from Turso failed (the hmm strategies predict the missing sessions themselves): %v", err))
		} else {
			note(fmt.Sprintf("hmm_prediction: loaded %d stored session(s) from Turso", n))
		}
		after = append(after, func() {
			if n, err := modelstate.PersistHMMModelAssetToTurso(ctx, db, path); err != nil {
				note(fmt.Sprintf("hmm_prediction: save to Turso failed: %v", err))
			} else {
				note(fmt.Sprintf("hmm_prediction: saved %d new session(s) to Turso", n))
			}
		})
	} else {
		note(fmt.Sprintf("hmm_prediction: no model file at %s, nothing to load from Turso", path))
	}

	if path := appenv.MarkovDB(); modelFileExists(path) {
		if n, err := modelstate.HydrateMarkovModelAssetFromTurso(ctx, db, path); err != nil {
			note(fmt.Sprintf("markov_extension: load from Turso failed (the markov strategies predict the missing sessions themselves): %v", err))
		} else {
			note(fmt.Sprintf("markov_extension: loaded %d stored session(s) from Turso", n))
		}
		after = append(after, func() {
			if n, err := modelstate.PersistMarkovModelAssetToTurso(ctx, db, path); err != nil {
				note(fmt.Sprintf("markov_extension: save to Turso failed: %v", err))
			} else {
				note(fmt.Sprintf("markov_extension: saved %d new session(s) to Turso", n))
			}
		})
	} else {
		note(fmt.Sprintf("markov_extension: no model file at %s, nothing to load from Turso", path))
	}

	return func() {
		for _, f := range after {
			f()
		}
	}
}

func modelFileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

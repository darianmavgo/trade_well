package strategies

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
)

// bundledStrategies is a reference database holding only the strategy rows this
// app trades. The full one (refdata/strategies.db, about 65,000 rows) is too big to
// ship, and a strategy that is a row in it (streak, markov, tree, hold) cannot be
// resolved without its row. Regenerate it from backtestgosqlite whenever the
// allowlist gains a strategy that is such a row:
//
//	strategy export -ids "<allowlist entries>" -out pkg/strategies/deploy/strategies.db -models-out pkg/strategies/deploy
//
// The same command writes the trained models those rows read, holding only their
// signal symbols: markov_models.db (train markov) and tree_models.db (train tree).
// The full model databases are far too big to ship (markov_models.db is over 500 MB).
//
//go:embed deploy/strategies.db
var bundledStrategies []byte

//go:embed deploy/markov_models.db
var bundledMarkovModels []byte

//go:embed deploy/tree_models.db
var bundledTreeModels []byte

// bundledHMMModels is the trained QQQ hidden Markov model and its predictions to the day it
// was exported (backtestgosqlite `train hmm export`; scripts/export_strategies.sh runs it).
// The hmm strategies extend it with the saved parameters and never refit it.
//
//go:embed deploy/hmm_models.db
var bundledHMMModels []byte

var (
	registerMu   sync.Mutex
	registeredAt string // the reference database the families were last registered from
)

// ensureRegistered makes the row-backed strategies resolvable through
// strategy.Get, and puts the trained models they read where backtestgosqlite looks
// for them. Where there is no reference database it writes the bundled one to
// the path backtestgosqlite reads ($APP_FOLDER/refdata/strategies.db), and the
// bundled markov and tree models to $APP_FOLDER/data/{markov,tree}_models.db. On
// App Engine each is always rewritten, since the instance's /tmp is empty or from
// an older deploy. Elsewhere an existing file is the full database and is left alone.
func ensureRegistered() error {
	registerMu.Lock()
	defer registerMu.Unlock()
	path := appenv.RefDB()
	if registeredAt == path {
		return nil
	}
	for _, f := range []struct {
		what string
		path string
		data []byte
	}{
		{"strategies", path, bundledStrategies},
		{"markov models", appenv.MarkovDB(), bundledMarkovModels},
		{"tree models", appenv.TreeDB(), bundledTreeModels},
		{"hmm models", appenv.HMMDB(), bundledHMMModels},
	} {
		if err := placeBundled(f.what, f.path, f.data); err != nil {
			return err
		}
	}
	stratreg.RegisterFamiliesAt(path)
	registeredAt = path
	return nil
}

// EnsureBundle places the bundled databases (strategies, markov, tree and hmm models) where
// backtestgosqlite reads them, exactly as the first strategy lookup does. Jobs that read a
// bundled model without resolving a strategy (the ready check) call it first.
func EnsureBundle() error { return ensureRegistered() }

// placeBundled writes a bundled database to path when none is there, and always on
// App Engine.
func placeBundled(what, path string, data []byte) error {
	if _, err := os.Stat(path); err == nil && !runtimeenv.Detect().IsCloud() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write bundled %s to %s: %w", what, path, err)
	}
	return nil
}

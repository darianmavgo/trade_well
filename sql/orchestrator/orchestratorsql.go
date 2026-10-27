// Package orchestratorsql gives the orchestrator its SQL: every statement and script its packages
// run, one file each, in sql/orchestrator/<package>/<name>.sql, where <package> is the
// pkg/orchestrator package that runs it. The backtest layer's SQL is in the folders beside this one
// (sql/strategies, sql/stages, ...) and is not mixed in here.
//
// The files are read from the file system, not embedded in the binary (see package sqlfiles for how
// the sql/ folder is found, on a laptop and on App Engine). A package reads its SQL by name:
//
//	orchestratorsql.Q("manage_exits/close_active_trade")   one statement, as text
//	orchestratorsql.Dir("live_vs_backtest_winrate")        all of a package's files, as an fs.FS
//
// A script of several stages is one file per stage in the package's folder. SQL a package builds at
// run time (a table name, an IN list of unknown length) is the one thing that may stay in Go;
// TestOrchestratorSQLLivesInFiles (pkg/orchestrator/sqliteguard) lists every such place so the list
// can only shrink. Nothing in the backtest layer may import this package (pkg/layers).
package orchestratorsql

import (
	"fmt"
	"io/fs"
	"os"
	"sync"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

var (
	mu    sync.Mutex
	texts = map[string]string{}
)

// init stops a process that cannot see the sql folder at once, with the reason and nothing else.
// The orchestrator's packages read their SQL at start-up and on every job, so there is nothing
// useful it could do without it; failing here beats a stack trace from a package that happened to
// read first. Packages that import this one initialise after it.
func init() {
	if _, err := sqlfiles.Root(); err != nil {
		fmt.Fprintf(os.Stderr, "orchestrator: %v\n", err)
		os.Exit(1)
	}
}

// Q returns the text of sql/orchestrator/<name>.sql, with name like "manage_exits/close_active_trade".
// A file is read once and kept. A name with no file, or no sql folder, is a deploy or programming
// error, so it panics with the reason rather than returning an empty statement.
func Q(name string) string {
	mu.Lock()
	defer mu.Unlock()
	if t, ok := texts[name]; ok {
		return t
	}
	b, err := fs.ReadFile(sqlfiles.Sub("orchestrator"), name+".sql")
	if err != nil {
		panic(fmt.Sprintf("orchestratorsql: sql/orchestrator/%s.sql: %v", name, err))
	}
	texts[name] = string(b)
	return texts[name]
}

// Dir returns the files of one package's folder as an fs.FS rooted there, so a file is named by its
// base name: fs.ReadFile(orchestratorsql.Dir("live_vs_backtest_winrate"), "02_slice_backtest.sql").
func Dir(pkg string) fs.FS { return sqlfiles.Sub("orchestrator/" + pkg) }

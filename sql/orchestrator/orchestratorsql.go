// Package orchestratorsql gives the orchestrator its SQL, all of it under sql/orchestrator and
// named by the pkg/orchestrator package that runs it. The backtest layer's SQL is in the folders
// beside this one (sql/strategies, sql/stages, ...) and is not mixed in here.
//
// Two shapes, each in one place:
//
//	sql/orchestrator/<package>.sql     the package's statements, each a block that starts with a
//	                                   "-- name: <name>" line (read with Q)
//	sql/orchestrator/<package>/*.sql   a script of stages or views, one file each, run in order
//	                                   (read with Dir, or File for one)
//
// A package reads a statement by name, and a stage by file name:
//
//	orchestratorsql.Q("manage_exits/close_active_trade")     the block in manage_exits.sql
//	orchestratorsql.File("trade_history", "view_win_rate")   trade_history/view_win_rate.sql
//	orchestratorsql.Dir("live_vs_backtest_winrate")          all of that folder, as an fs.FS
//
// The files are read from the file system, not embedded in the binary (package sqlfiles has the one
// rule for finding the sql folder, and its errors say where it looked). SQL a package builds at run
// time (a table name, an IN list of unknown length) is the one thing that may stay in Go;
// TestOrchestratorSQLLivesInFiles (pkg/orchestrator/sqliteguard) lists every such place so the list
// can only shrink. Nothing in the backtest layer may import this package (pkg/layers).
package orchestratorsql

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

var (
	mu       sync.Mutex
	packages = map[string]map[string]string{} // package -> statement name -> text
	files    = map[string]string{}            // "<package>/<name>" -> text of a stage file
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

var header = regexp.MustCompile(`^--\s*name:\s*([A-Za-z0-9_]+)\s*$`)

// parse splits the text of a <package>.sql file into its named statements.
func parse(pkg, text string) (map[string]string, error) {
	out := map[string]string{}
	var name string
	var body []string
	flush := func() {
		if name != "" {
			out[name] = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if m := header.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			flush()
			name, body = m[1], nil
			if _, dup := out[name]; dup {
				return nil, fmt.Errorf("sql/orchestrator/%s.sql has two statements named %s", pkg, name)
			}
			out[name] = ""
			continue
		}
		body = append(body, line)
	}
	flush()
	return out, nil
}

// Q returns the statement called name in sql/orchestrator/<package>.sql, with the argument written
// "<package>/<name>", for example "manage_exits/close_active_trade". A file is read and split once.
// A statement or a file that is not there, or no sql folder, is a deploy or programming error, so it
// panics with the reason (and the sql folder error says where it looked) rather than returning an
// empty statement.
func Q(name string) string {
	pkg, stmt, ok := strings.Cut(name, "/")
	if !ok {
		panic(fmt.Sprintf("orchestratorsql: Q(%q): want \"<package>/<name>\"", name))
	}
	mu.Lock()
	defer mu.Unlock()
	statements, ok := packages[pkg]
	if !ok {
		b, err := fs.ReadFile(sqlfiles.Sub("orchestrator"), pkg+".sql")
		if err != nil {
			panic(fmt.Sprintf("orchestratorsql: sql/orchestrator/%s.sql: %v", pkg, err))
		}
		statements, err = parse(pkg, string(b))
		if err != nil {
			panic("orchestratorsql: " + err.Error())
		}
		packages[pkg] = statements
	}
	text, ok := statements[stmt]
	if !ok || text == "" {
		panic(fmt.Sprintf("orchestratorsql: sql/orchestrator/%s.sql has no statement named %q", pkg, stmt))
	}
	return text
}

// File returns the text of the stage file sql/orchestrator/<package>/<name>.sql.
func File(pkg, name string) string {
	key := pkg + "/" + name
	mu.Lock()
	defer mu.Unlock()
	if t, ok := files[key]; ok {
		return t
	}
	b, err := fs.ReadFile(sqlfiles.Sub("orchestrator/"+pkg), name+".sql")
	if err != nil {
		panic(fmt.Sprintf("orchestratorsql: sql/orchestrator/%s/%s.sql: %v", pkg, name, err))
	}
	files[key] = string(b)
	return files[key]
}

// Dir returns one package's folder of stage files as an fs.FS rooted there, so a file is named by
// its base name: fs.ReadFile(orchestratorsql.Dir("live_vs_backtest_winrate"), "02_slice_backtest.sql").
func Dir(pkg string) fs.FS { return sqlfiles.Sub("orchestrator/" + pkg) }

// Names returns the names of the statements in sql/orchestrator/<package>.sql, for tests that check
// every statement is read by something.
func Names(pkg string) ([]string, error) {
	b, err := fs.ReadFile(sqlfiles.Sub("orchestrator"), pkg+".sql")
	if err != nil {
		return nil, err
	}
	m, err := parse(pkg, string(b))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return names, nil
}

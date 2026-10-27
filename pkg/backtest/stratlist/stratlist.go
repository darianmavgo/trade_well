// Package stratlist runs a SELECT from a .sql file against a database
// (default refdata/strategies.db) and returns the first column of every row as
// a list of strategy ids. The list can be fed to `backtest -strategy`.
//
// The query decides which strategies are picked; see sql/lists/ for examples.
// The database is opened query-only, so a list file cannot change data.
package stratlist

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// Config holds every setting of a list run.
type Config struct {
	DB      string // database the query runs against; empty = appenv.RefDB()
	SQLFile string // path to the .sql file holding one SELECT
}

// DefaultConfig returns the defaults the stratlist CLI uses.
func DefaultConfig() Config { return Config{DB: appenv.RefDB()} }

// List runs the SELECT in cfg.SQLFile and returns the first column of each
// row, in query order, with blanks and duplicates removed. The query must
// return the strategy id as its first column.
func List(ctx context.Context, cfg Config) ([]string, error) {
	if cfg.SQLFile == "" {
		return nil, fmt.Errorf("stratlist: no sql file given")
	}
	if cfg.DB == "" {
		cfg.DB = appenv.RefDB()
	}
	if fi, err := os.Stat(cfg.DB); err != nil || fi.Size() == 0 {
		return nil, fmt.Errorf("stratlist: database %s not found or empty", cfg.DB)
	}
	query, err := os.ReadFile(cfg.SQLFile)
	if err != nil {
		return nil, fmt.Errorf("stratlist: %w", err)
	}

	db, err := storage.OpenSQLite(cfg.DB)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// query_only is per connection, so pin one.
	conn, err := db.Connx(ctx)
	if err != nil {
		return nil, fmt.Errorf("stratlist: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, fmt.Errorf("stratlist: %w", err)
	}

	rows, err := conn.QueryContext(ctx, string(query))
	if err != nil {
		return nil, fmt.Errorf("stratlist: %s: %w", cfg.SQLFile, err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	var ids []string
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("stratlist: %w", err)
		}
		id := strings.TrimSpace(fmt.Sprint(vals[0]))
		if vals[0] == nil || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stratlist: %s: %w", cfg.SQLFile, err)
	}
	return ids, nil
}

// Main is the CLI entry point: parse flags, call List, print the ids.
// Positional arg 1 is the SQL file when -sql is not given.
func Main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run is Main without os.Args/os.Exit so tests can call it. It returns the
// process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	cfg := DefaultConfig()
	var comma bool
	fs := flag.NewFlagSet("stratlist", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.DB, "db", cfg.DB, "SQLite database the query runs against")
	fs.StringVar(&cfg.SQLFile, "sql", "", "SQL file holding one SELECT whose first column is the strategy id (or pass it as the first argument)")
	fs.BoolVar(&comma, "comma", false, "print one comma-separated line, ready for `backtest -strategy`, instead of one id per line")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if cfg.SQLFile == "" && fs.NArg() > 0 {
		cfg.SQLFile = fs.Arg(0)
	}

	ids, err := List(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if comma {
		fmt.Fprintln(stdout, strings.Join(ids, ","))
	} else {
		for _, id := range ids {
			fmt.Fprintln(stdout, id)
		}
	}
	return 0
}

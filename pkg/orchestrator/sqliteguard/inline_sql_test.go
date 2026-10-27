package sqliteguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// dynamicSQL lists the files that still build SQL text in Go, with how many statements each builds.
// Each is SQL whose text is not fixed: a table name chosen at run time, an IN list of unknown
// length, a WHERE clause added when a filter is given, a LIMIT formatted in, a statement joined to
// a Go constant. Everything else lives in sql/orchestrator (package orchestratorsql). The list must
// match what is in the code exactly, so it can only shrink: when a file stops building SQL, delete
// its line; when new SQL is needed, put it in a file.
var dynamicSQL = map[string]int{
	"pkg/orchestrator/clear_stale/clear_stale.go":                           2, // an UPDATE joined to the status constants
	"pkg/orchestrator/dashboard/manage_exit_page.go":                        1, // a LIMIT formatted in
	"pkg/orchestrator/dashboard/server.go":                                  1, // a table name, a WHERE clause and a LIMIT formatted in
	"pkg/orchestrator/live_vs_backtest_report/fills.go":                     2, // a table name chosen by the caller
	"pkg/orchestrator/live_vs_backtest_report/run.go":                       1, // a table name and an optional WHERE
	"pkg/orchestrator/live_vs_backtest_trades/backfill.go":                  2, // a table name chosen by the caller
	"pkg/orchestrator/live_vs_backtest_winrate/live_vs_backtest_winrate.go": 1, // ALTER TABLE ... ADD COLUMN with a column name and type from a list
	"pkg/orchestrator/modelstate/bars.go":                                   1, // a table name chosen by the caller
	"pkg/orchestrator/run_e2e_live/run_e2e_live.go":                         2, // a table name chosen by the caller
	"pkg/orchestrator/runtables/runtables.go":                               3, // DROP TABLE of a table whose name is the run's
	"pkg/orchestrator/scan_market/sqlite_model_asset_trace.go":              1, // an IN list of unknown length
	"pkg/orchestrator/scan_market/sqlite_working_copy_market.go":            3, // an IN list, and a multi-row VALUES list, of unknown length
	"pkg/orchestrator/scan_market/trace.go":                                 1, // an optional WHERE clause
	"pkg/orchestrator/strategies/sqlite_strategy_asset_bundle_info.go":      1, // a table name from a list
}

var (
	sqlStart = regexp.MustCompile(`^\s*(SELECT|INSERT|UPDATE|DELETE|CREATE|DROP|ALTER|WITH|PRAGMA|REPLACE)\s`)
	sqlShape = regexp.MustCompile(`(?s)^\s*(SELECT\s.*\sFROM\s|SELECT\s+[0-9]|SELECT\s+(COUNT|MAX|MIN|date|datetime)\b|INSERT\s+(OR\s+\w+\s+)?INTO\s|UPDATE\s+\w+\s+SET\s|DELETE\s+FROM\s|CREATE\s+(UNIQUE\s+)?(TABLE|INDEX|VIEW|TRIGGER)\s|DROP\s+(TABLE|VIEW|INDEX)\s|ALTER\s+TABLE\s|WITH\s.*\sAS\s*\(|PRAGMA\s|REPLACE\s+INTO\s)`)
)

func TestOrchestratorSQLLivesInFiles(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	found := map[string]int{}
	for _, dir := range []string{"pkg/orchestrator", "cmd/orchestrator"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil || !sqlStart.MatchString(text) || !sqlShape.MatchString(text) || len(strings.TrimSpace(text)) < 12 {
					return true
				}
				// a regular expression over SQL text is not SQL
				if len(stack) >= 2 {
					if call, ok := stack[len(stack)-2].(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
							if x, ok := sel.X.(*ast.Ident); ok && x.Name == "regexp" {
								return true
							}
						}
					}
				}
				rel, _ := filepath.Rel(root, path)
				found[filepath.ToSlash(rel)]++
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var problems []string
	for file, n := range found {
		if dynamicSQL[file] != n {
			problems = append(problems, fmt.Sprintf("%s builds %d SQL statement(s) in Go, the list allows %d", file, n, dynamicSQL[file]))
		}
	}
	for file, n := range dynamicSQL {
		if found[file] == 0 {
			problems = append(problems, fmt.Sprintf("%s is on the list (%d) but no longer builds SQL in Go: remove it", file, n))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		var current []string
		for file, n := range found {
			current = append(current, fmt.Sprintf("\t%q: %d,", file, n))
		}
		sort.Strings(current)
		t.Fatalf("SQL belongs in sql/orchestrator/<package>/<name>.sql, read with orchestratorsql.Q (see sql/orchestrator).\n%s\nWhat is in the code now:\n%s",
			strings.Join(problems, "\n"), strings.Join(current, "\n"))
	}
}

// What the code reads and what is on disk agree, in both directions. A name in the code with no SQL
// behind it would otherwise fail only when that statement first runs on a live instance, and SQL
// nothing reads is clutter.
//
//	orchestratorsql.Q("<package>/<name>")   needs a "-- name: <name>" block in sql/orchestrator/<package>.sql,
//	                                        and every block must be read by some Q
//	orchestratorsql.File("<package>", n)    needs sql/orchestrator/<package>/<n>.sql
//	orchestratorsql.Dir("<package>")        reads the whole folder, so all of its files count as read
//
// A File whose name is not a literal (a helper that takes the name) counts its package's whole folder as read.
func TestEveryOrchestratorSQLNameExistsAndIsRead(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	statements := map[string]string{} // "<package>/<name>" -> where it is read
	stageFiles := map[string]string{} // "<package>/<name>" -> where it is read
	wholeFolder := map[string]bool{}
	for _, dir := range []string{"pkg/orchestrator", "cmd/orchestrator"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "orchestratorsql" {
					return true
				}
				where := fset.Position(call.Pos()).String()
				lit := func(e ast.Expr) (string, bool) {
					b, ok := e.(*ast.BasicLit)
					if !ok {
						return "", false
					}
					v, err := strconv.Unquote(b.Value)
					return v, err == nil
				}
				switch sel.Sel.Name {
				case "Q":
					if name, ok := lit(call.Args[0]); ok {
						statements[name] = where
					}
				case "Dir":
					if pkg, ok := lit(call.Args[0]); ok {
						wholeFolder[pkg] = true
					}
				case "File":
					if len(call.Args) < 2 {
						return true
					}
					pkg, ok := lit(call.Args[0])
					if !ok {
						return true
					}
					if name, ok := lit(call.Args[1]); ok {
						stageFiles[pkg+"/"+name] = where
					} else {
						wholeFolder[pkg] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	var problems []string
	sqlRoot := filepath.Join(root, "sql", "orchestrator")
	have := map[string]bool{} // "<package>/<name>" of every block on disk
	entries, err := os.ReadDir(sqlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		pkg := strings.TrimSuffix(e.Name(), ".sql")
		names, err := orchestratorsql.Names(pkg)
		if err != nil {
			problems = append(problems, fmt.Sprintf("sql/orchestrator/%s.sql: %v", pkg, err))
			continue
		}
		for _, n := range names {
			have[pkg+"/"+n] = true
			if _, read := statements[pkg+"/"+n]; !read {
				problems = append(problems, fmt.Sprintf("sql/orchestrator/%s.sql: the statement %q is read by nothing", pkg, n))
			}
		}
	}
	for name, where := range statements {
		if !have[name] {
			problems = append(problems, fmt.Sprintf("%s reads the statement %s, which is not in sql/orchestrator", where, name))
		}
	}
	for name, where := range stageFiles {
		if _, err := os.Stat(filepath.Join(sqlRoot, filepath.FromSlash(name)+".sql")); err != nil {
			problems = append(problems, fmt.Sprintf("%s reads sql/orchestrator/%s.sql, which does not exist", where, name))
		}
	}
	err = filepath.WalkDir(sqlRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		rel, _ := filepath.Rel(sqlRoot, path)
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if len(parts) == 1 { // <package>.sql: its statements were checked above
			return nil
		}
		if _, read := stageFiles[strings.TrimSuffix(rel, ".sql")]; !read && !wholeFolder[parts[0]] {
			problems = append(problems, fmt.Sprintf("sql/orchestrator/%s is read by nothing", rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("orchestrator SQL and the code that reads it disagree:\n%s", strings.Join(problems, "\n"))
	}
}

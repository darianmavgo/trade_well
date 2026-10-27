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

// Every orchestratorsql.Q("<package>/<name>") in the code names a file that exists, and every file in
// sql/orchestrator is read by something. A typo in a name would otherwise show only when that
// statement first runs on a live instance. A package that reads its folder as a whole
// (orchestratorsql.Dir, or Q with a name built at run time) counts all of its files as read.
func TestEveryOrchestratorSQLFileExistsAndIsRead(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	named := map[string]string{} // "<package>/<name>" -> where it is read
	wholePackage := map[string]bool{}
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
				switch arg := call.Args[0].(type) {
				case *ast.BasicLit:
					name, _ := strconv.Unquote(arg.Value)
					if sel.Sel.Name == "Dir" {
						wholePackage[name] = true
					} else {
						named[name] = where
					}
				case *ast.BinaryExpr: // "<package>/" + name: the package is read by name at run time
					if lit, ok := arg.X.(*ast.BasicLit); ok {
						prefix, _ := strconv.Unquote(lit.Value)
						wholePackage[strings.TrimSuffix(prefix, "/")] = true
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
	for name, where := range named {
		if _, err := fs.Stat(osDir(root), filepath.ToSlash(filepath.Join("sql", "orchestrator", name+".sql"))); err != nil {
			problems = append(problems, fmt.Sprintf("%s reads sql/orchestrator/%s.sql, which does not exist", where, name))
		}
	}
	err := filepath.WalkDir(filepath.Join(root, "sql", "orchestrator"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(root, "sql", "orchestrator"), path)
		rel = filepath.ToSlash(rel)
		pkg := strings.Split(rel, "/")[0]
		if _, ok := named[strings.TrimSuffix(rel, ".sql")]; !ok && !wholePackage[pkg] {
			problems = append(problems, fmt.Sprintf("sql/orchestrator/%s is read by nothing", rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("orchestrator SQL files and the code that reads them disagree:\n%s", strings.Join(problems, "\n"))
	}
}

func osDir(root string) fs.FS { return os.DirFS(root) }

package sqliteguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fileKind = regexp.MustCompile(`^sqlite_(strategy_asset|model_asset|backtest_result_asset|working_copy)(_[a-z0-9_]+)?\.go$`)

// kindWords are the words a function name must contain, one per kind of file.
var kindWords = map[string]string{
	"strategy_asset":        "Asset",
	"model_asset":           "Asset",
	"backtest_result_asset": "Asset",
	"working_copy":          "WorkingCopy",
}

// opensSQLite reports whether call opens a SQLite database: sql.Open("sqlite", ...), or one of
// backtestgosqlite's openers.
func opensSQLite(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, _ := sel.X.(*ast.Ident)
	if pkg == nil {
		return false
	}
	switch {
	case pkg.Name == "sql" && sel.Sel.Name == "Open":
		if len(call.Args) > 0 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok {
				return strings.Trim(lit.Value, `"`) == "sqlite"
			}
		}
	case pkg.Name == "storage" && (sel.Sel.Name == "OpenSQLite" || sel.Sel.Name == "OpenSQLiteReadOnly"):
		return true
	case pkg.Name == "dailybars" && sel.Sel.Name == "Open":
		return true
	// backtestgosqlite functions that open a SQLite file on the caller's behalf.
	case pkg.Name == "train" && sel.Sel.Name == "ReadHMMLatest":
		return true
	case (pkg.Name == "markov_strategy" || pkg.Name == "hmm_strategy") && sel.Sel.Name == "ModelLastDate":
		return true
	case pkg.Name == "storage" && sel.Sel.Name == "SymbolLastDate":
		return true
	case pkg.Name == "dailybars" && sel.Sel.Name == "LastDate":
		return true
	case pkg.Name == "refdb":
		return true
	}
	return false
}

func TestSQLiteIsOpenedOnlyByFunctionsNamedForTheirKind(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	var offenders []string
	for _, dir := range []string{"pkg/orchestrator", "cmd"} {
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
			base := filepath.Base(path)
			m := fileKind.FindStringSubmatch(base)
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == "modernc.org/sqlite" && m == nil {
					offenders = append(offenders, path+": imports the SQLite driver but the file is not named sqlite_<kind>[_<what>].go")
				}
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				opens := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok && opensSQLite(c) {
						opens = true
					}
					return !opens
				})
				if !opens {
					continue
				}
				switch {
				case m == nil:
					offenders = append(offenders, path+": "+fn.Name.Name+" opens SQLite but the file is not named sqlite_<kind>[_<what>].go")
				case !strings.Contains(fn.Name.Name, kindWords[m[1]]):
					offenders = append(offenders, path+": "+fn.Name.Name+" opens SQLite but its name does not contain "+kindWords[m[1]])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("SQLite is opened outside the Asset / WorkingCopy / Scratch scheme (see doc.go):\n%s", strings.Join(offenders, "\n"))
	}
}

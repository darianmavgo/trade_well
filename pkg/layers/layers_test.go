package layers

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/darianmavgo/trade_well/"

// forbidden lists, for each layer directory, the module paths it must not import.
var forbidden = map[string][]string{
	"pkg/schwaber":     {"pkg/backtest", "pkg/orchestrator", "sql", "pkg/layers"},
	"pkg/backtest":     {"pkg/schwaber", "pkg/orchestrator", "sql/orchestrator", "pkg/layers"},
	"sql":              {"pkg", "cmd"},
	"pkg/orchestrator": {"pkg/layers"},
}

func TestImportsPointDown(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var offenders []string
	for layer, banned := range forbidden {
		err := filepath.WalkDir(filepath.Join(root, layer), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if !strings.HasPrefix(p, module) {
					continue
				}
				rel := strings.TrimPrefix(p, module)
				for _, b := range banned {
					if rel == b || strings.HasPrefix(rel, b+"/") {
						offenders = append(offenders, filepath.ToSlash(path)+" imports "+rel)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("a lower layer imports upward or sideways (see doc.go):\n%s", strings.Join(offenders, "\n"))
	}
}

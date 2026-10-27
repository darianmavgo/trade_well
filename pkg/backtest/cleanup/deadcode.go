package cleanup

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ScanDead returns the Go packages (relative directories) that no main
// package reaches through imports. Test files do not count as a use.
func ScanDead(ctx context.Context, cfg Config) ([]string, error) {
	mod, err := modulePath(cfg.Root)
	if err != nil {
		return nil, err
	}
	imports := map[string][]string{} // package dir -> imported package dirs in this module
	var mains []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(cfg.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if refSkipDir[d.Name()] && p != cfg.Root || d.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, e := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if e != nil {
			return nil
		}
		dir, _ := filepath.Rel(cfg.Root, filepath.Dir(p))
		if _, ok := imports[dir]; !ok {
			imports[dir] = nil
		}
		if f.Name.Name == "main" && !contains(mains, dir) {
			mains = append(mains, dir)
		}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			if path == mod || strings.HasPrefix(path, mod+"/") {
				rel := strings.TrimPrefix(strings.TrimPrefix(path, mod), "/")
				if rel == "" {
					rel = "."
				}
				imports[dir] = append(imports[dir], rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	stack := append([]string(nil), mains...)
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[d] {
			continue
		}
		seen[d] = true
		stack = append(stack, imports[d]...)
	}
	var dead []string
	for d := range imports {
		if !seen[d] {
			dead = append(dead, d)
		}
	}
	sort.Strings(dead)
	return dead, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func modulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module line in %s/go.mod", root)
}

// Dead prints the packages no main package reaches and returns how many it
// found. It never deletes: removing code is a decision, not a sweep.
func Dead(ctx context.Context, cfg Config) (int, error) {
	dead, err := ScanDead(ctx, cfg)
	if err != nil {
		return 0, err
	}
	for _, d := range dead {
		fmt.Fprintf(cfg.Out, "unreachable package %s\n", d)
	}
	fmt.Fprintf(cfg.Out, "found: %d packages no main package imports (delete by hand)\n", len(dead))
	return len(dead), nil
}

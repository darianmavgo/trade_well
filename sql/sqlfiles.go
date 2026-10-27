// Package sqlfiles gives the code access to the SQL in this folder, read from the file system.
//
// The SQL is not embedded in the binary. A command run from the repository, a test, and a deployed
// App Engine instance all have the sql/ folder next to the code (a deploy uploads it, see
// .gcloudignore), and there is one rule for finding it: the folder named sql, holding this
// repository's SQL (stages/ or orchestrator/), in the working directory or the nearest parent
// that has one. If there is none, every read fails with an error that says where it looked.
//
// FS is the folder as an fs.FS, so a file is named from it: "stages/check/01.sql",
// "strategies/<pipeline>/<file>.sql", "validation/check_overfit.sql", "views/<file>.sql".
// Editing a file takes effect on the next run, with no rebuild.
package sqlfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu   sync.Mutex
	root string
)

// isSQLRoot reports whether dir is a sql folder of this repository.
func isSQLRoot(dir string) bool {
	for _, marker := range []string{"stages", "orchestrator"} {
		if st, err := os.Stat(filepath.Join(dir, marker)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// searchUp looks for a sql folder in start and each of its parents.
func searchUp(start string) string {
	for dir := start; ; dir = filepath.Dir(dir) {
		if candidate := filepath.Join(dir, "sql"); isSQLRoot(candidate) {
			return candidate
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// Root returns the sql folder, found by the rule in the package comment. A folder that was found is
// remembered; when there is none, each call searches again and returns an error that names the
// working directory it searched from and what to do.
func Root() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if root != "" {
		return root, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot find the sql folder: the working directory is unknown: %w", err)
	}
	if r := searchUp(wd); r != "" {
		root = r
		return root, nil
	}
	return "", fmt.Errorf("cannot find the sql folder: there is no folder named sql (holding stages/ or orchestrator/) in the working directory %s or any parent. "+
		"The SQL is read from files, not embedded in the binary: run from the repository root, or make sure the deploy uploads sql/ (see .gcloudignore)", wd)
}

// Resolve turns a path the code names relative to the repository ("sql/strategies/mara_tree") into
// a path on disk: relative to the folder that holds the sql folder. An absolute path is returned as
// it is. It fails with the error of Root when the sql folder cannot be found.
func Resolve(p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(r), p), nil
}

// disk is a folder under the sql root, opened when first used so that merely importing this
// package never fails.
type disk struct{ sub string }

// Open implements fs.FS.
func (d disk) Open(name string) (fs.File, error) {
	r, err := Root()
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return os.DirFS(filepath.Join(r, d.sub)).Open(name)
}

// FS is the sql folder.
var FS fs.FS = disk{}

// Sub is the folder dir under the sql folder, as an fs.FS rooted there.
func Sub(dir string) fs.FS { return disk{sub: dir} }

// SymbolStatsView returns view_symbol_stats.sql: the v_symbol_stats view over backtest_start, created in
// every market database by storage.EnsureBarTable.
func SymbolStatsView() (string, error) {
	b, err := fs.ReadFile(FS, "view_symbol_stats.sql")
	return string(b), err
}

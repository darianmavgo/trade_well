package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

// KeepCalc keeps the per-strategy SQL calculation databases (the slice tables
// a pipeline builds) under <out-dir>/calc/ so any stage can be queried. Off by
// default: they are scratch files, thousands of them in a bulk run, and are
// deleted when the run finishes. Set by backtest -keep-calc.
var KeepCalc bool

// calcDBPath returns the calc database path for one strategy run and a cleanup
// function to call when the run is done.
func calcDBPath(outDir, id string) (string, func()) {
	dir, cleanup := calcDir(outDir)
	return freshCalcFile(filepath.Join(dir, fmt.Sprintf("calc_%s.db", id))), cleanup
}

// freshCalcFile removes a calculation database (and its WAL and SHM files) left by an earlier run
// and returns its path. The pipelines build their tables IF NOT EXISTS, so a kept file from an
// earlier run would hand its rows to this one (see "No shared or lasting scratch tables").
func freshCalcFile(path string) string {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
	return path
}

// calcDir returns a directory for calc databases and its cleanup function. With
// KeepCalc it is <outDir>/calc and cleanup does nothing; otherwise it is a
// fresh temporary directory removed (WAL and SHM files included) by cleanup.
func calcDir(outDir string) (string, func()) {
	if KeepCalc {
		dir := filepath.Join(outDir, "calc")
		_ = os.MkdirAll(dir, 0o755)
		return dir, func() {}
	}
	dir, err := os.MkdirTemp("", "bt-calc-")
	if err != nil {
		// Writing scratch tables into the reports folder instead would leave them behind for
		// the next run to find, so stop.
		panic(fmt.Sprintf("runner: cannot create a temporary folder for the calculation databases: %v (check free space and permissions on the OS temp folder, or run with -keep-calc to use <out-dir>/calc)", err))
	}
	return dir, func() { _ = os.RemoveAll(dir) }
}

// CalcDBPath is calcDBPath for packages outside runner (gridsearch builds its
// own slice tables). Call the returned cleanup when the run is done.
func CalcDBPath(outDir, id string) (string, func()) { return calcDBPath(outDir, id) }

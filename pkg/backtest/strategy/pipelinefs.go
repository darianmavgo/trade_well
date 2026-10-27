package strategy

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// A pipeline directory is normally a path like "sql/strategies/mara_tree",
// relative to the process working directory. That only exists when the process
// runs from a backtestgosqlite checkout. readPipelineDir/readPipelineFile use
// the directory on disk when it exists (repo-root runs, and editing SQL without
// rebuilding) and otherwise fall back to the copy embedded in the binary
// (sqlfiles.Strategies), keyed by the directory's final element. Library
// callers therefore run the same SQL as the CLI, from any working directory.

func pipelineOnDisk(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func embeddedPipelinePath(dir string) string {
	return path.Join("strategies", filepath.Base(dir))
}

// readPipelineDir lists the entries of a pipeline directory.
func readPipelineDir(dir string) ([]fs.DirEntry, error) {
	if pipelineOnDisk(dir) {
		return os.ReadDir(dir)
	}
	entries, err := fs.ReadDir(sqlfiles.Strategies, embeddedPipelinePath(dir))
	if err != nil {
		return nil, fmt.Errorf("pipeline dir %q not found on disk or embedded: %w", dir, err)
	}
	return entries, nil
}

// readPipelineFile reads one file of a pipeline directory.
func readPipelineFile(dir, name string) ([]byte, error) {
	if pipelineOnDisk(dir) {
		return os.ReadFile(filepath.Join(dir, name))
	}
	return fs.ReadFile(sqlfiles.Strategies, path.Join(embeddedPipelinePath(dir), name))
}

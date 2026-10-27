package strategy

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// A pipeline directory is a path like "sql/strategies/mara_tree": relative to the folder that holds
// the sql folder (found from the working directory, see sqlfiles), or absolute. It is read from disk
// and nowhere else, so editing the SQL needs no rebuild; a directory that is not there is an error
// that names the path it looked at.

// pipelinePath is the directory on disk.
func pipelinePath(dir string) (string, error) {
	p, err := sqlfiles.Resolve(dir)
	if err != nil {
		return "", fmt.Errorf("pipeline directory %q: %w", dir, err)
	}
	return p, nil
}

// readPipelineDir lists the entries of a pipeline directory.
func readPipelineDir(dir string) ([]fs.DirEntry, error) {
	p, err := pipelinePath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("pipeline directory %q (%s): %w", dir, p, err)
	}
	return entries, nil
}

// readPipelineFile reads one file of a pipeline directory.
func readPipelineFile(dir, name string) ([]byte, error) {
	p, err := pipelinePath(dir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(p, name))
	if err != nil {
		return nil, fmt.Errorf("pipeline file %q in %q (%s): %w", name, dir, p, err)
	}
	return b, nil
}

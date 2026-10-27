package cleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OrphanFile is an entry in data/ or refdata/ whose name no Go or shell file mentions.
type OrphanFile struct {
	Path  string // relative to the app folder
	Bytes int64
	Dir   bool
}

// ScanData lists the top-level entries of data/ and refdata/ whose name no
// .go or .sh file mentions. The reports folder is skipped (runs and orphans
// tidy it), as are -wal, -shm and -journal files, which follow their database.
// Names built at run time are invisible, so the result is a list of candidates.
func ScanData(ctx context.Context, cfg Config) ([]OrphanFile, error) {
	text, err := goSources(ctx, cfg.Root)
	if err != nil {
		return nil, err
	}
	files, err := corpus(cfg.Root)
	if err != nil {
		return nil, err
	}
	for p, t := range files {
		if filepath.Ext(p) == ".sh" {
			text += "\n" + t
		}
	}
	var out []OrphanFile
	for _, folder := range []string{"data", "refdata"} {
		entries, err := os.ReadDir(filepath.Join(cfg.Root, folder))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if name == "reports" && folder == "data" || strings.HasPrefix(name, ".") {
				continue
			}
			base := name
			for _, suf := range []string{"-wal", "-shm", "-journal"} {
				base = strings.TrimSuffix(base, suf)
			}
			if strings.Contains(text, base) {
				continue
			}
			full := filepath.Join(cfg.Root, folder, name)
			o := OrphanFile{Path: filepath.Join(folder, name), Dir: e.IsDir()}
			if e.IsDir() {
				o.Bytes = dirBytes(full)
			} else if info, err := e.Info(); err == nil {
				o.Bytes = info.Size()
			}
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Data prints the unreferenced entries of data/ and refdata/ and returns how
// many it found. It never deletes: a database is not rebuilt from code.
func Data(ctx context.Context, cfg Config) (int, error) {
	orphans, err := ScanData(ctx, cfg)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, o := range orphans {
		kind := "file"
		if o.Dir {
			kind = "dir "
		}
		fmt.Fprintf(cfg.Out, "unreferenced %s %-45s %12d bytes\n", kind, o.Path, o.Bytes)
		total += o.Bytes
	}
	fmt.Fprintf(cfg.Out, "found: %d unreferenced data entries, %d bytes (delete by hand)\n", len(orphans), total)
	return len(orphans), nil
}

package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StrayBin is a compiled executable that should not be there.
type StrayBin struct {
	Path   string // relative to the app folder
	Bytes  int64
	Reason string
}

var binMagic = [][]byte{
	{0x7f, 'E', 'L', 'F'},
	{0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf}, // Mach-O
	{0xce, 0xfa, 0xed, 0xfe}, {0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe}, // universal
	{'M', 'Z'},
}

func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := f.Read(head)
	for _, m := range binMagic {
		if n >= len(m) && bytes.HasPrefix(head[:n], m) {
			return true
		}
	}
	return false
}

var binSkip = map[string]bool{".git": true, "data": true, "refdata": true, "node_modules": true, "vendor": true}

// ScanBins finds executables outside bin/ (a bare go build leaves one in the
// current folder) and in bin/ with no cmd/<name> behind them.
func ScanBins(ctx context.Context, cfg Config) ([]StrayBin, error) {
	cmds, err := subdirs(cfg.Root, "cmd")
	if err != nil {
		return nil, err
	}
	isCmd := set(cmds)
	binDir := filepath.Join(cfg.Root, "bin")
	var out []StrayBin
	err = filepath.WalkDir(cfg.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if binSkip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || !isBinary(p) {
			return nil
		}
		rel, _ := filepath.Rel(cfg.Root, p)
		switch {
		case filepath.Dir(p) != binDir:
			out = append(out, StrayBin{rel, info.Size(), "executable outside bin/"})
		case !isCmd[strings.TrimSuffix(d.Name(), ".exe")]:
			out = append(out, StrayBin{rel, info.Size(), "no cmd/" + d.Name()})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// Bins prints the stray executables and, when cfg.Remove is set and cfg.DryRun
// is not, deletes them. It returns how many it found.
func Bins(ctx context.Context, cfg Config) (int, error) {
	stray, err := ScanBins(ctx, cfg)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, b := range stray {
		fmt.Fprintf(cfg.Out, "stray binary %-40s %10d bytes  (%s)\n", b.Path, b.Bytes, b.Reason)
		total += b.Bytes
	}
	verb := "found"
	if cfg.Remove && !cfg.DryRun {
		for _, b := range stray {
			if err := os.Remove(filepath.Join(cfg.Root, b.Path)); err != nil {
				return len(stray), err
			}
		}
		verb = "removed"
	} else if cfg.Remove {
		verb = "would remove"
	}
	fmt.Fprintf(cfg.Out, "%s: %d stray binaries, %d bytes\n", verb, len(stray), total)
	return len(stray), nil
}

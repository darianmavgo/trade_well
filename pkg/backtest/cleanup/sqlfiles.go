package cleanup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// StaleSQL is one unit under sql/ that no Go file, markdown or shell script
// mentions: a file, or a whole directory for stages, strategies and the
// omnifunds_reverse study.
type StaleSQL struct {
	Path  string // relative to the sql folder
	Bytes int64
	Dir   bool
}

var corpusExt = map[string]bool{".go": true, ".md": true, ".sh": true}
var corpusSkip = map[string]bool{".git": true, "data": true, "bin": true, "refdata": true, "node_modules": true, "vendor": true}

// sqlUnits lists what is judged: directories under stages/, strategies/ and
// studies/ (one unit each), every other .sql file. README.md and embed.go stay.
func sqlUnits(sqlDir string) ([]StaleSQL, error) {
	var units []StaleSQL
	err := filepath.WalkDir(sqlDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == sqlDir {
			return err
		}
		rel, _ := filepath.Rel(sqlDir, p)
		parts := strings.Split(rel, string(filepath.Separator))
		dirUnit := d.IsDir() && ((len(parts) == 2 && (parts[0] == "stages" || parts[0] == "strategies")) ||
			(len(parts) == 2 && parts[0] == "studies"))
		if dirUnit {
			units = append(units, StaleSQL{Path: rel, Bytes: dirBytes(p), Dir: true})
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".sql") {
			info, err := d.Info()
			if err != nil {
				return err
			}
			units = append(units, StaleSQL{Path: rel, Bytes: info.Size()})
		}
		return nil
	})
	return units, err
}

func dirBytes(dir string) (n int64) {
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// corpus reads every .go, .md and .sh file under root (the app folder's
// parent, so sibling repos count), keyed by path.
func corpus(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if corpusSkip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !corpusExt[filepath.Ext(p)] {
			return nil
		}
		if info, e := d.Info(); e != nil || info.Size() > 4<<20 {
			return nil
		}
		b, e := os.ReadFile(p)
		if e == nil {
			files[p] = string(b)
		}
		return nil
	})
	return files, err
}

// ScanSQL returns the units under <Root>/sql that nothing refers to. A file
// is referenced when its base name appears in another file; a directory when
// its name appears as a whole word in a file outside it. Names built at
// run time are invisible, so the result is a list of candidates.
func ScanSQL(cfg Config) ([]StaleSQL, error) {
	sqlDir := filepath.Join(cfg.Root, "sql")
	units, err := sqlUnits(sqlDir)
	if err != nil {
		return nil, err
	}
	files, err := corpus(filepath.Dir(filepath.Clean(cfg.Root)))
	if err != nil {
		return nil, err
	}
	var stale []StaleSQL
	for _, u := range units {
		full := filepath.Join(sqlDir, u.Path)
		var re *regexp.Regexp
		if u.Dir {
			re = regexp.MustCompile(`\b` + regexp.QuoteMeta(filepath.Base(u.Path)) + `\b`)
		}
		found := false
		for p, text := range files {
			if p == full || (u.Dir && strings.HasPrefix(p, full+string(filepath.Separator))) {
				continue
			}
			if u.Dir && re.MatchString(text) || !u.Dir && strings.Contains(text, filepath.Base(u.Path)) {
				found = true
				break
			}
		}
		if !found {
			stale = append(stale, u)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].Path < stale[j].Path })
	return stale, nil
}

// SQL lists the unreferenced units under sql/ and, when cfg.RemoveSQL is set
// and cfg.DryRun is not, deletes them. It returns how many it found.
func SQL(cfg Config) (int, error) {
	stale, err := ScanSQL(cfg)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, s := range stale {
		kind := "file"
		if s.Dir {
			kind = "dir "
		}
		fmt.Fprintf(cfg.Out, "unreferenced %s %-50s %6d bytes\n", kind, filepath.Join("sql", s.Path), s.Bytes)
		total += s.Bytes
	}
	verb := "found"
	if cfg.RemoveSQL && !cfg.DryRun {
		for _, s := range stale {
			if err := os.RemoveAll(filepath.Join(cfg.Root, "sql", s.Path)); err != nil {
				return len(stale), err
			}
		}
		verb = "removed"
	} else if cfg.RemoveSQL {
		verb = "would remove"
	}
	fmt.Fprintf(cfg.Out, "%s: %d unreferenced sql units, %d bytes\n", verb, len(stale), total)
	return len(stale), nil
}

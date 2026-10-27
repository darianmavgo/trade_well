// Package clear_older removes the older versions of a backtest result
// database. Result DBs are written per run as <id>.db, <id>_2.db, <id>_3.db
// (see storage); given the version to keep, every earlier version goes,
// together with its -wal and -shm files.
package clear_older

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
)

// MaxWorkers caps the number of concurrent deleters.
const MaxWorkers = 32

var versionRe = regexp.MustCompile(`^(.+)_(\d+)$`)

var sidecars = []string{"", "-wal", "-shm"}

// Config holds the settings of a run.
type Config struct {
	Keep    []string      // result DBs to keep; everything older is cleared
	Workers int           // -workers, capped at MaxWorkers
	DryRun  bool          // -dry-run
	Dir     string        // reports folder, scanned for stale items
	Stale   time.Duration // when > 0, clear what has not changed for this long instead of old versions
	Out     io.Writer
}

// Main parses flags and runs.
func Main() {
	cfg := Config{Out: os.Stdout}
	flag.IntVar(&cfg.Workers, "workers", MaxWorkers, "concurrent deleters (max 32)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "list the files that would be removed without removing them")
	dir := flag.String("dir", appenv.Reports(), "reports folder scanned when no file is given")
	staleDays := flag.Int("stale-days", 0, "instead of old versions, clear result DBs and numbered pipeline run folders in -dir untouched for this many days")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: clear_older [-workers N] [-dry-run] [-dir reports] [<reports/id_5.db> ...]")
		fmt.Fprintln(os.Stderr, "with no file, keeps the newest version of every result DB in -dir")
		flag.PrintDefaults()
	}
	flag.Parse()
	cfg.Keep = flag.Args()
	cfg.Dir = *dir
	cfg.Stale = time.Duration(*staleDays) * 24 * time.Hour
	if cfg.Stale == 0 && len(cfg.Keep) == 0 {
		var err error
		if cfg.Keep, err = Latest(*dir); err != nil {
			fmt.Fprintln(os.Stderr, "clear_older:", err)
			os.Exit(1)
		}
	}
	n, err := Run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clear_older:", err)
		os.Exit(1)
	}
	verb := "removed"
	if cfg.DryRun {
		verb = "would remove"
	}
	fmt.Fprintf(cfg.Out, "%s %d files\n", verb, n)
}

// newest is the latest modification time of path, or of anything under it.
func newest(path string) time.Time {
	var t time.Time
	filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
		return nil
	})
	return t
}

// pipelineLocked reports whether a pipeline run holds the run folder.
func pipelineLocked(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "pipeline.lock"))
	if err != nil {
		return false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	p, err := os.FindProcess(pid)
	return pid > 0 && err == nil && p.Signal(syscall.Signal(0)) == nil
}

// Stale returns what in dir has not been modified since cutoff: the files of
// each result DB (the .db with its -wal and -shm go together, judged by the
// newest of them) and each numbered pipeline run folder (judged by the newest
// file under it; a folder locked by a live pipeline is skipped).
func Stale(dir string, cutoff time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if _, err := strconv.Atoi(e.Name()); err != nil || pipelineLocked(p) {
				continue
			}
			if newest(p).Before(cutoff) {
				out = append(out, p)
			}
		case strings.HasSuffix(e.Name(), ".db"):
			var group []string
			var latest time.Time
			for _, sc := range sidecars {
				if info, err := os.Lstat(p + sc); err == nil {
					group = append(group, p+sc)
					if info.ModTime().After(latest) {
						latest = info.ModTime()
					}
				}
			}
			if latest.Before(cutoff) {
				out = append(out, group...)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Latest returns the newest version of every result DB directly in dir.
func Latest(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil {
		return nil, err
	}
	best := map[string]int{}
	file := map[string]string{}
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".db")
		base, ver := name, 1
		if m := versionRe.FindStringSubmatch(name); m != nil {
			base = m[1]
			ver, _ = strconv.Atoi(m[2])
		}
		if ver >= best[base] {
			best[base], file[base] = ver, p
		}
	}
	out := make([]string, 0, len(file))
	for _, p := range file {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Older returns the existing files of every version older than keep:
// <id>.db counts as version 1, <id>_N.db as version N. The kept file must
// exist. Returns the paths sorted.
func Older(keep string) ([]string, error) {
	if _, err := os.Stat(keep); err != nil {
		return nil, err
	}
	dir := filepath.Dir(keep)
	name := strings.TrimSuffix(filepath.Base(keep), ".db")
	if name == filepath.Base(keep) {
		return nil, fmt.Errorf("%s: not a .db file", keep)
	}
	base, ver := name, 1
	if m := versionRe.FindStringSubmatch(name); m != nil {
		base = m[1]
		ver, _ = strconv.Atoi(m[2])
	}
	var out []string
	for v := 1; v < ver; v++ {
		stem := base
		if v > 1 {
			stem = base + "_" + strconv.Itoa(v)
		}
		for _, s := range sidecars {
			p := filepath.Join(dir, stem+".db"+s)
			if _, err := os.Lstat(p); err == nil {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Run clears the older versions of each kept DB and returns the number of
// files removed (or that would be, on a dry run).
func Run(cfg Config) (int, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	workers := cfg.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > MaxWorkers {
		workers = MaxWorkers
	}
	var files []string
	if cfg.Stale > 0 {
		var err error
		if files, err = Stale(cfg.Dir, time.Now().Add(-cfg.Stale)); err != nil {
			return 0, err
		}
	}
	for _, k := range cfg.Keep {
		if cfg.Stale > 0 {
			break
		}
		f, err := Older(k)
		if err != nil {
			return 0, err
		}
		files = append(files, f...)
	}
	if cfg.DryRun {
		for _, f := range files {
			fmt.Fprintln(cfg.Out, f)
		}
		return len(files), nil
	}

	jobs := make(chan string)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		removed int
		errs    []error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				err := os.RemoveAll(f)
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					removed++
					fmt.Fprintln(cfg.Out, "removed", f)
				}
				mu.Unlock()
			}
		}()
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	if len(errs) > 0 {
		return removed, fmt.Errorf("%d removals failed, first: %w", len(errs), errs[0])
	}
	return removed, nil
}

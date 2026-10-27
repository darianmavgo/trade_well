package cleanup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// StaleRef is one place in a doc, script or config that names something the
// tree no longer has.
type StaleRef struct {
	File string // relative to the app folder
	Line int
	Text string
}

var (
	refExt     = map[string]bool{".md": true, ".sh": true, ".yaml": true, ".yml": true, ".toml": true}
	cmdRef     = regexp.MustCompile("(?:^|[\\s\"'`(=:]|\\./)(?:bin|cmd)/([a-z][a-z0-9_]*)\\b")
	pkgRef     = regexp.MustCompile(`\bpkg/([a-z][a-z0-9_]*(?:/[a-z][a-z0-9_]*)*)`)
	cronRef    = regexp.MustCompile(`/cron/([a-z][a-z0-9_]*)`)
	envAssign  = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)=`)
	notACmd    = map[string]bool{"bash": true, "sh": true, "zsh": true, "env": true, "python": true, "python3": true, "local": true}
	refSkipDir = map[string]bool{".git": true, "data": true, "bin": true, "refdata": true, "node_modules": true, "vendor": true}
)

// ScanRefs reads every .md, .sh, .yaml, .yml and .toml file under the app
// folder and reports mentions of: a bin/ or cmd/ name with no cmd/<name>,
// a pkg/ path that does not exist, a /cron/<job> with no pkg/orchestrator/<job>,
// and a NAME= environment assignment no Go file mentions. It changes nothing.
// A mention can be deliberate history, so the result is a list of candidates.
func ScanRefs(ctx context.Context, cfg Config) ([]StaleRef, error) {
	cmds, err := subdirs(cfg.Root, "cmd")
	if err != nil {
		return nil, err
	}
	jobs, err := JobNames(cfg.Root)
	if err != nil {
		return nil, err
	}
	isCmd, isJob := set(cmds), set(jobs)

	goText, err := goSources(ctx, cfg.Root)
	if err != nil {
		return nil, err
	}
	var refs []StaleRef
	err = filepath.WalkDir(cfg.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if refSkipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !refExt[filepath.Ext(p)] {
			return nil
		}
		if info, e := d.Info(); e != nil || info.Size() > 4<<20 {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(cfg.Root, p)
		for i, line := range strings.Split(string(b), "\n") {
			add := func(f string, a ...any) { refs = append(refs, StaleRef{rel, i + 1, fmt.Sprintf(f, a...)}) }
			for _, m := range cmdRef.FindAllStringSubmatch(line, -1) {
				if !isCmd[m[1]] && !notACmd[m[1]] {
					add("names bin/cmd %q but there is no cmd/%s", m[1], m[1])
				}
			}
			for _, m := range pkgRef.FindAllStringSubmatch(line, -1) {
				if !pkgExists(cfg.Root, m[1]) {
					add("names pkg/%s which does not exist", m[1])
				}
			}
			for _, m := range cronRef.FindAllStringSubmatch(line, -1) {
				if !isJob[m[1]] {
					add("names /cron/%s but there is no pkg/orchestrator/%s", m[1], m[1])
				}
			}
			for _, m := range envAssign.FindAllStringSubmatch(line, -1) {
				if !strings.Contains(goText, m[1]) {
					add("sets %s which no Go file mentions", m[1])
				}
			}
		}
		return nil
	})
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].Line < refs[j].Line
	})
	return refs, err
}

// pkgExists is true for a directory, or a file that is the path plus an extension.
func pkgExists(root, rel string) bool {
	p := filepath.Join(root, "pkg", filepath.FromSlash(rel))
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return true
	}
	m, _ := filepath.Glob(p + ".*")
	return len(m) > 0
}

// goSources is the text of every .go file under root, joined, for substring checks.
func goSources(ctx context.Context, root string) (string, error) {
	var sb strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if refSkipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) == ".go" {
			if b, e := os.ReadFile(p); e == nil {
				sb.Write(b)
				sb.WriteByte('\n')
			}
		}
		return nil
	})
	return sb.String(), err
}

// Refs prints the stale references and returns how many it found.
func Refs(ctx context.Context, cfg Config) (int, error) {
	refs, err := ScanRefs(ctx, cfg)
	if err != nil {
		return 0, err
	}
	for _, r := range refs {
		fmt.Fprintf(cfg.Out, "%s:%d: %s\n", r.File, r.Line, r.Text)
	}
	fmt.Fprintf(cfg.Out, "found: %d stale references\n", len(refs))
	return len(refs), nil
}

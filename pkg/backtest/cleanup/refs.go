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
	From string // text to replace on that line, when the right replacement is certain
	To   string
}

// Deletable reports whether Refs -remove drops the whole line: a markdown line
// that names something that is gone. Scripts and config are never line-deleted,
// since removing a line of code would break them.
func (r StaleRef) Deletable() bool { return !r.Fixable() && filepath.Ext(r.File) == ".md" }

// Fixable reports whether Refs -remove can rewrite this mention.
func (r StaleRef) Fixable() bool { return r.From != "" }

// layers are the folders under pkg/ that the flattened repos became.
var layers = []string{"backtest", "orchestrator", "schwaber"}

// relocated finds the one layer folder that holds pkg/<rel>, the way a path
// written before the repos were flattened (pkg/strategy) maps to pkg/backtest/strategy.
func relocated(root, rel string) (string, bool) {
	var hit []string
	for _, l := range layers {
		if pkgExists(root, l+"/"+rel) {
			hit = append(hit, l)
		}
	}
	if len(hit) != 1 {
		return "", false
	}
	return hit[0], true
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
			add := func(f string, a ...any) {
				refs = append(refs, StaleRef{File: rel, Line: i + 1, Text: fmt.Sprintf(f, a...)})
			}
			for _, m := range cmdRef.FindAllStringSubmatch(line, -1) {
				if !isCmd[m[1]] && !notACmd[m[1]] {
					add("names bin/cmd %q but there is no cmd/%s", m[1], m[1])
				}
			}
			for _, m := range pkgRef.FindAllStringSubmatch(line, -1) {
				if pkgExists(cfg.Root, m[1]) {
					continue
				}
				if l, ok := relocated(cfg.Root, m[1]); ok {
					refs = append(refs, StaleRef{File: rel, Line: i + 1, From: "pkg/" + m[1], To: "pkg/" + l + "/" + m[1],
						Text: fmt.Sprintf("names pkg/%s, now pkg/%s/%s", m[1], l, m[1])})
				} else {
					add("names pkg/%s which does not exist", m[1])
				}
			}
			for _, m := range cronRef.FindAllStringSubmatch(line, -1) {
				if !isJob[m[1]] {
					add("names /cron/%s but there is no pkg/orchestrator/%s", m[1], m[1])
				}
			}
			for _, m := range envAssign.FindAllStringSubmatch(line, -1) {
				if filepath.Ext(p) == ".sh" {
					break // a NAME= in a script is usually a shell variable, not configuration
				}
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

// Refs prints the stale references and returns how many it found. With
// cfg.Remove set and cfg.DryRun not, it rewrites the ones that have one right
// answer (a pkg path that moved under pkg/backtest, pkg/orchestrator or
// pkg/schwaber). The rest name something that is gone, so a person decides
// whether to delete the sentence or point it elsewhere.
func Refs(ctx context.Context, cfg Config) (int, error) {
	refs, err := ScanRefs(ctx, cfg)
	if err != nil {
		return 0, err
	}
	fixable, deletable := 0, 0
	for _, r := range refs {
		mark := ""
		if r.Fixable() {
			fixable++
			mark = " [fixable]"
		} else if r.Deletable() {
			deletable++
			mark = " [line deletable]"
		}
		fmt.Fprintf(cfg.Out, "%s:%d: %s%s\n", r.File, r.Line, r.Text, mark)
	}
	verb := "found"
	if cfg.Remove && !cfg.DryRun {
		files, err := fixRefs(cfg.Root, refs)
		if err != nil {
			return len(refs), err
		}
		verb = fmt.Sprintf("rewrote %d paths and deleted %d lines in %d files; found", fixable, deletable, files)
	} else if cfg.Remove {
		verb = fmt.Sprintf("would rewrite %d paths and delete %d lines; found", fixable, deletable)
	}
	fmt.Fprintf(cfg.Out, "%s: %d stale references, %d need a person (script or config lines)\n", verb, len(refs), len(refs)-fixable-deletable)
	return len(refs), nil
}

// fixRefs applies every fixable reference to its line and returns how many files changed.
func fixRefs(root string, refs []StaleRef) (int, error) {
	byFile := map[string][]StaleRef{}
	for _, r := range refs {
		if r.Fixable() || r.Deletable() {
			byFile[r.File] = append(byFile[r.File], r)
		}
	}
	changed := 0
	for file, fixes := range byFile {
		path := filepath.Join(root, file)
		info, err := os.Stat(path)
		if err != nil {
			return changed, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return changed, err
		}
		lines := strings.Split(string(b), "\n")
		drop := map[int]bool{}
		for _, f := range fixes {
			if f.Deletable() {
				drop[f.Line-1] = true
			}
		}
		for _, f := range fixes {
			if !f.Fixable() || drop[f.Line-1] {
				continue
			}
			lines[f.Line-1] = replacePath(lines[f.Line-1], f.From, f.To)
		}
		kept := lines[:0:0]
		for i, l := range lines {
			if !drop[i] {
				kept = append(kept, l)
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), info.Mode().Perm()); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// replacePath swaps from for to wherever from is a whole path: pkg/strategy
// changes in "pkg/strategy/*.go" but not in the start of "pkg/strategy/family"
// or "pkg/strategy_cmd".
func replacePath(line, from, to string) string {
	var sb strings.Builder
	for {
		i := strings.Index(line, from)
		if i < 0 {
			sb.WriteString(line)
			return sb.String()
		}
		end := i + len(from)
		rest := line[end:]
		more := len(rest) > 0 && (isPathByte(rest[0]) || rest[0] == '/' && len(rest) > 1 && isPathByte(rest[1]))
		sb.WriteString(line[:i])
		if more {
			sb.WriteString(from)
		} else {
			sb.WriteString(to)
		}
		line = rest
	}
}

func isPathByte(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' }

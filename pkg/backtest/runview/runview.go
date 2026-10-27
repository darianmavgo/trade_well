// Package runview is a local, read-only browser for a run folder under
// data/reports: the pipeline steps, every result database with its tables,
// the HTML reports, and a SQL box. Nothing it opens can be written.
package runview

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed web
var webFS embed.FS

// Config holds the settings of a run.
type Config struct {
	Root     string // -root, the reports folder
	Addr     string // -addr
	Open     bool   // -open
	Ref      string // -ref, the strategy reference database
	DB       string // -db, the market database
	Universe string // -universe
}

// DefaultConfig returns the CLI defaults.
func DefaultConfig() Config {
	return Config{Root: appenv.Reports(), Addr: "127.0.0.1:8765", Open: true, Ref: appenv.RefDB(), DB: appenv.MarketDB(), Universe: appenv.UniverseDB()}
}

// Main is the CLI entry point.
func Main() {
	conf := DefaultConfig()
	flag.StringVar(&conf.Root, "root", conf.Root, "reports folder that holds the numbered run folders")
	flag.StringVar(&conf.Addr, "addr", conf.Addr, "listen address (keep it on 127.0.0.1: the viewer has no login)")
	flag.StringVar(&conf.Ref, "ref", conf.Ref, "strategy reference database (strategies.db), read for strategy definitions; empty to skip")
	flag.StringVar(&conf.DB, "db", conf.DB, "market database, read to rank ETFs by dollar volume")
	flag.StringVar(&conf.Universe, "universe", conf.Universe, "universe database (which symbols are ETFs)")
	flag.BoolVar(&conf.Open, "open", conf.Open, "open the browser")
	flag.Parse()
	if err := Run(conf); err != nil {
		log.Fatal(err)
	}
}

// Run serves the viewer until the process stops.
func Run(conf Config) error {
	s := &server{root: conf.Root, refDB: conf.Ref, marketDB: conf.DB, universeDB: conf.Universe}
	ln, err := net.Listen("tcp", conf.Addr)
	if err != nil {
		return err
	}
	u := "http://" + ln.Addr().String()
	fmt.Println("runview:", u, " reading", conf.Root)
	if conf.Open {
		openBrowser(u)
	}
	return http.Serve(ln, s.routes())
}

func openBrowser(u string) {
	name := map[string]string{"darwin": "open", "windows": "explorer"}[runtime.GOOS]
	if name == "" {
		name = "xdg-open"
	}
	_ = exec.Command(name, u).Start()
}

type server struct {
	root       string
	refDB      string // refdata/strategies.db, may be empty
	marketDB   string
	universeDB string
	etf        etfCache
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/runs", s.handle(s.runs))
	mux.HandleFunc("/api/run", s.handle(s.run))
	mux.HandleFunc("/api/db", s.handle(s.dbInfo))
	mux.HandleFunc("/api/table", s.handle(s.table))
	mux.HandleFunc("/api/strategy", s.handle(s.strategy))
	mux.HandleFunc("/api/etfs", s.handle(s.etfs))
	mux.HandleFunc("/api/symbol", s.handle(s.symbol))
	mux.HandleFunc("/api/find", s.handle(s.find))
	mux.HandleFunc("/api/query", s.handle(s.query))
	mux.HandleFunc("/file/", s.file)
	return mux
}

func (s *server) handle(f func(url.Values) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q url.Values
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			q = r.PostForm
		} else {
			q = r.URL.Query()
		}
		out, err := f(q)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			out = map[string]string{"error": err.Error()}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

var runDirRe = regexp.MustCompile(`^[0-9]+$`)

// resolve maps a run id and a relative file to a path inside the run folder.
func (s *server) resolve(runID, rel string) (string, error) {
	if !runDirRe.MatchString(runID) {
		return "", fmt.Errorf("bad run %q", runID)
	}
	base := filepath.Join(s.root, runID)
	p := filepath.Join(base, filepath.FromSlash(rel))
	if p != base && !strings.HasPrefix(p, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path leaves the run folder")
	}
	return p, nil
}

// --- runs ---

type runInfo struct {
	ID       int       `json:"id"`
	Modified time.Time `json:"modified"`
	Live     bool      `json:"live"` // a pipeline.lock is present
	Steps    int       `json:"steps"`
	Done     int       `json:"done"`
	Failed   int       `json:"failed"`
}

func (s *server) runs(url.Values) (any, error) {
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var out []runInfo
	for _, e := range ents {
		if !e.IsDir() || !runDirRe.MatchString(e.Name()) {
			continue
		}
		id, _ := strconv.Atoi(e.Name())
		info := runInfo{ID: id}
		if fi, err := e.Info(); err == nil {
			info.Modified = fi.ModTime()
		}
		dir := filepath.Join(s.root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "pipeline.lock")); err == nil {
			info.Live = true
		}
		if steps, err := readSteps(filepath.Join(dir, "pipeline.db")); err == nil {
			info.Steps = len(steps)
			for _, st := range steps {
				switch st.Status {
				case "done":
					info.Done++
				case "failed":
					info.Failed++
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// --- one run ---

type step struct {
	Seq     int    `db:"seq" json:"seq"`
	Name    string `db:"name" json:"name"`
	Status  string `db:"status" json:"status"`
	Started string `db:"started_at" json:"started"`
	Ended   string `db:"ended_at" json:"ended"`
	Error   string `db:"error" json:"error"`
}

func openRO(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func readSteps(path string) ([]step, error) {
	db, err := openRO(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var out []step
	err = db.Select(&out, `SELECT seq, name, status, COALESCE(started_at,'') AS started_at,
		COALESCE(finished_at,'') AS ended_at, COALESCE(error,'') AS error FROM pipeline_step ORDER BY seq`)
	return out, err
}

type fileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"` // db or html
}

func (s *server) run(q url.Values) (any, error) {
	dir, err := s.resolve(q.Get("run"), "")
	if err != nil {
		return nil, err
	}
	var files []fileInfo
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		kind := map[string]string{".db": "db", ".html": "html"}[strings.ToLower(filepath.Ext(p))]
		if kind == "" {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if fi, err := d.Info(); err == nil {
			files = append(files, fileInfo{Path: filepath.ToSlash(rel), Size: fi.Size(), Kind: kind})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	steps, _ := readSteps(filepath.Join(dir, "pipeline.db"))
	var syms, strats int
	if db, err := openRO(filepath.Join(dir, "pipeline.db")); err == nil {
		_ = db.Get(&syms, `SELECT count(*) FROM pipeline_scope WHERE kind='symbol'`)
		_ = db.Get(&strats, `SELECT count(*) FROM pipeline_scope WHERE kind='strategy'`)
		db.Close()
	}
	_, live := os.Stat(filepath.Join(dir, "pipeline.lock"))
	return map[string]any{"files": files, "steps": steps, "symbols": syms, "strategies": strats, "live": live == nil}, nil
}

// --- databases ---

func (s *server) openDB(q url.Values) (*sqlx.DB, error) {
	p, err := s.resolve(q.Get("run"), q.Get("file"))
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Ext(p), ".db") {
		return nil, fmt.Errorf("not a database")
	}
	return openRO(p)
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

type colInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	PK   bool   `json:"pk"`
}

func tableColumns(db *sqlx.DB, table string) ([]colInfo, error) {
	rows, err := db.Queryx("PRAGMA table_info(" + quoteIdent(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []colInfo
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, colInfo{Name: name, Type: strings.ToUpper(typ), PK: pk > 0})
	}
	return cols, rows.Err()
}

// countCap bounds a row count so a billion-row table does not stall the page.
const countCap = 2_000_000

func (s *server) dbInfo(q url.Values) (any, error) {
	db, err := s.openDB(q)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	type tbl struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Rows   int64  `json:"rows"`
		Capped bool   `json:"capped"`
		Cols   int    `json:"cols"`
	}
	var master []struct {
		Name string `db:"name"`
		Type string `db:"type"`
	}
	if err := db.Select(&master, `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY type DESC, name`); err != nil {
		return nil, err
	}
	var out []tbl
	for _, m := range master {
		t := tbl{Name: m.Name, Kind: m.Type, Rows: -1}
		if cols, err := tableColumns(db, m.Name); err == nil {
			t.Cols = len(cols)
		}
		if q.Get("counts") != "0" {
			var n int64
			if err := db.Get(&n, fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s LIMIT %d)", quoteIdent(m.Name), countCap)); err == nil {
				t.Rows, t.Capped = n, n >= countCap
			}
		}
		out = append(out, t)
	}
	return out, nil
}

const maxPage = 1000

func (s *server) table(q url.Values) (any, error) {
	db, err := s.openDB(q)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	table := q.Get("table")
	cols, err := tableColumns(db, table)
	if err != nil || len(cols) == 0 {
		return nil, fmt.Errorf("no table %q", table)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > maxPage {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	var where []string
	var args []any
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		var parts []string
		for _, c := range cols {
			parts = append(parts, "CAST("+quoteIdent(c.Name)+" AS TEXT) LIKE ?")
			args = append(args, "%"+search+"%")
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	known := map[string]bool{}
	for _, c := range cols {
		known[c.Name] = true
	}
	for key, vals := range q { // f.<column>=<text>: that column contains the text
		if name, ok := strings.CutPrefix(key, "f."); ok && known[name] && vals[0] != "" {
			where = append(where, "CAST("+quoteIdent(name)+" AS TEXT) LIKE ?")
			args = append(args, "%"+vals[0]+"%")
		}
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	order := ""
	if sort := q.Get("sort"); known[sort] {
		dir := "ASC"
		if q.Get("dir") == "desc" {
			dir = "DESC"
		}
		order = " ORDER BY " + quoteIdent(sort) + " " + dir
	}
	var total int64
	cq := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s%s LIMIT %d)", quoteIdent(table), clause, countCap)
	if err := db.Get(&total, cq, args...); err != nil {
		return nil, err
	}
	rows, err := db.Queryx(fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d", quoteIdent(table), clause, order, limit, offset), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data, _, err := scanRows(rows, maxPage)
	if err != nil {
		return nil, err
	}
	return map[string]any{"columns": cols, "rows": data, "total": total, "capped": total >= countCap, "limit": limit, "offset": offset}, nil
}

// scanRows turns rows into slices of JSON-safe values; blobs become a size
// note. It stops at max rows and reports whether more were left.
func scanRows(rows *sqlx.Rows, max int) (out [][]any, truncated bool, err error) {
	n, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	out = [][]any{}
	for rows.Next() {
		if len(out) >= max {
			return out, true, nil
		}
		vals := make([]any, len(n))
		ptrs := make([]any, len(n))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = fmt.Sprintf("<%d bytes>", len(b))
			}
		}
		out = append(out, vals)
	}
	return out, false, rows.Err()
}

var readOnlySQL = regexp.MustCompile(`(?is)^\s*(select|with|pragma\s+table_info|explain)\b`)

func (s *server) query(q url.Values) (any, error) {
	sqlText := strings.TrimSpace(q.Get("sql"))
	if !readOnlySQL.MatchString(sqlText) {
		return nil, fmt.Errorf("only SELECT, WITH and EXPLAIN statements run here")
	}
	db, err := s.openDB(q)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	start := time.Now()
	rows, err := db.Queryx(sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, _ := rows.Columns()
	cols := make([]colInfo, len(names))
	for i, n := range names {
		cols[i] = colInfo{Name: n}
	}
	data, truncated, err := scanRows(rows, maxPage)
	if err != nil {
		return nil, err
	}
	return map[string]any{"columns": cols, "rows": data, "total": len(data), "truncated": truncated, "ms": time.Since(start).Milliseconds()}, nil
}

// file serves an HTML report from a run folder: /file/<run>/<path>.html.
func (s *server) file(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/file/")
	runID, rel, ok := strings.Cut(rest, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := s.resolve(runID, rel)
	if err != nil || !strings.EqualFold(filepath.Ext(p), ".html") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

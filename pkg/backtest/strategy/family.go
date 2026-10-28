package strategy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/jmoiron/sqlx"
)

// Family is a kind of strategy whose members are rows in a table (streak,
// hold, ...). The family is registered once; a member is built from its row
// when Get asks for it. No row is registered individually.
type Family interface {
	// Name is the family name, for logs ("streak").
	Name() string
	// Lookup builds the member with this id, if the family has one.
	Lookup(id string) (Strategy, bool)
	// IDs lists every member id, ordered.
	IDs() ([]string, error)
}

var (
	familyLock sync.RWMutex
	families   []Family
)

// RegisterFamily adds a family to the lookup chain used by Get. A family with
// the same Name replaces the earlier one (so a test can point it at a temp DB).
func RegisterFamily(f Family) {
	familyLock.Lock()
	defer familyLock.Unlock()
	for i, old := range families {
		if old.Name() == f.Name() {
			families[i] = f
			return
		}
	}
	families = append(families, f)
}

// Families returns the registered families in registration order.
func Families() []Family {
	familyLock.RLock()
	defer familyLock.RUnlock()
	return append([]Family(nil), families...)
}

// exactLooker is implemented by a family that can answer an exact id cheaply.
type exactLooker interface {
	LookupExact(id string) (Strategy, bool)
}

// lookupFamilies tries every family for an exact id first (an indexed lookup in
// each), and only then lets a family match loosely (ignoring case and "-" "_"
// and spaces), which scans its table. Without the first pass an exact id would
// pay a table scan in every family that does not hold it.
func lookupFamilies(id string) (Strategy, bool) {
	fams := Families()
	for _, f := range fams {
		if e, ok := f.(exactLooker); ok {
			if s, ok := e.LookupExact(id); ok {
				return s, true
			}
		}
	}
	for _, f := range fams {
		if s, ok := f.Lookup(id); ok {
			return s, true
		}
	}
	return nil, false
}

// RowFamily is the Family for one table in the reference DB. Build turns the
// canonical id into a strategy (read the row, validate it); an error means
// the row is unusable and is logged.
type RowFamily struct {
	FamilyName string
	Table      string
	Path       func() string // reference DB path; read at lookup time
	Build      func(db *sqlx.DB, id string) (Strategy, bool, error)

	mu    sync.Mutex
	db    *sqlx.DB
	dbAt  string
	cache map[string]Strategy
}

func (f *RowFamily) Name() string { return f.FamilyName }

// open returns the shared handle, reopening when the path changed. Callers hold f.mu.
func (f *RowFamily) open() *sqlx.DB {
	path := f.Path()
	if f.db != nil && f.dbAt == path {
		return f.db
	}
	if f.db != nil {
		f.db.Close()
		f.db, f.cache = nil, nil
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		return nil
	}
	db, err := refdb.Open(path)
	if err != nil {
		return nil
	}
	f.db, f.dbAt, f.cache = db, path, map[string]Strategy{}
	return db
}

func (f *RowFamily) Lookup(id string) (Strategy, bool) { return f.lookup(id, true) }

// LookupExact is Lookup for an id that matches a stored id exactly.
func (f *RowFamily) LookupExact(id string) (Strategy, bool) { return f.lookup(id, false) }

func (f *RowFamily) lookup(id string, loose bool) (Strategy, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	db := f.open()
	if db == nil {
		return nil, false
	}
	if s, ok := f.cache[id]; ok {
		return s, true
	}
	var canon string
	var ok bool
	if loose {
		canon, ok = refdb.CanonicalID(db, f.Table, id)
	} else {
		canon, ok = refdb.ExactID(db, f.Table, id)
	}
	if !ok {
		return nil, false
	}
	if s, ok := f.cache[canon]; ok {
		f.cache[id] = s
		return s, true
	}
	s, ok, err := f.Build(db, canon)
	if err != nil {
		log.Printf("%s: skip %s: %v", f.Table, canon, err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	f.cache[canon], f.cache[id] = s, s
	return s, true
}

func (f *RowFamily) IDs() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	db := f.open()
	if db == nil {
		return nil, fmt.Errorf("%s: reference DB %s not found or empty", f.Table, f.Path())
	}
	return refdb.IDs(db, f.Table)
}

// ListAll returns every member of every family, built on demand, sorted by ID. These are the
// strategies defined in the reference database (strategies.db). A strategy registered in Go
// with no row there is left out: it is still found by its own id, but "all" never runs it. Use
// ListAll only where "all" must mean every row (a full backtest or scoreboard); listings should
// print List() and FamilyCounts instead.
func ListAll() []Strategy {
	var out []Strategy
	for _, f := range Families() {
		ids, err := f.IDs()
		if err != nil {
			log.Printf("%s: %v", f.Name(), err)
			continue
		}
		for _, id := range ids {
			if s, ok := f.Lookup(id); ok {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// FamilyCount is the number of rows one family holds.
type FamilyCount struct {
	Name  string
	Count int
}

// FamilyCounts reports how many members each family has, in registration order.
func FamilyCounts() []FamilyCount {
	var out []FamilyCount
	for _, f := range Families() {
		ids, err := f.IDs()
		if err != nil {
			continue
		}
		out = append(out, FamilyCount{Name: f.Name(), Count: len(ids)})
	}
	return out
}

// PrintFamilyCounts writes one line per family telling the reader that its
// rows are not listed and how to select them.
func PrintFamilyCounts(w io.Writer) {
	for _, fc := range FamilyCounts() {
		fmt.Fprintf(w, "  %-10s %6d rows in %s_strategy (not listed; select with `stratlist` or name an id)\n", fc.Name, fc.Count, fc.Name)
	}
}

// FamilyAxes holds the grid values gridsearch tries for each gridsearchable
// column of a family, from strategy_family_param (sql/stages/family_params).
type FamilyAxes struct {
	Nums map[string][]float64 // numeric columns
	Strs map[string][]string  // text columns
}

var familyAxesMu sync.Mutex

// LoadFamilyAxes reads the axes of family from the reference DB. A missing
// reference DB gives empty axes, so a caller falls back to the row's own value.
func LoadFamilyAxes(family string) FamilyAxes {
	// refdb.Open seeds the family parameter table, a write: concurrent sweeps
	// (gridsearch persymbol) would otherwise lock each other out.
	familyAxesMu.Lock()
	defer familyAxesMu.Unlock()
	ax := FamilyAxes{Nums: map[string][]float64{}, Strs: map[string][]string{}}
	if fi, err := os.Stat(refdb.DefaultPath); err != nil || fi.Size() == 0 {
		return ax
	}
	db, err := refdb.Open(refdb.DefaultPath)
	if err != nil {
		log.Printf("family axes %s: %v", family, err)
		return ax
	}
	defer db.Close()
	rows, err := refdb.FamilyParams(db, family)
	if err != nil {
		log.Printf("family axes %s: %v", family, err)
		return ax
	}
	for _, r := range rows {
		if !r.Gridsearchable || r.GridValues == nil {
			continue
		}
		var nums []float64
		if err := json.Unmarshal([]byte(*r.GridValues), &nums); err == nil {
			ax.Nums[r.Param] = nums
			continue
		}
		var strs []string
		if err := json.Unmarshal([]byte(*r.GridValues), &strs); err == nil {
			ax.Strs[r.Param] = strs
		}
	}
	return ax
}

// Ints returns a numeric axis as ints.
func (a FamilyAxes) Ints(param string) []int {
	var out []int
	for _, v := range a.Nums[param] {
		out = append(out, int(v))
	}
	return out
}

// FamilyMembers returns every member of the family named name (streak, hold,
// tree or markov), built on demand, and whether such a family exists.
func FamilyMembers(name string) ([]Strategy, bool) {
	for _, f := range Families() {
		if f.Name() != name {
			continue
		}
		ids, err := f.IDs()
		if err != nil {
			log.Printf("%s: %v", name, err)
			return nil, true
		}
		var out []Strategy
		for _, id := range ids {
			if s, ok := f.Lookup(id); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

// Familied is implemented by the strategies of a row-backed family.
type Familied interface {
	Family() string
}

// FamilyOf names the family a strategy belongs to: streak, hold, tree
// or markov for a row, and "builtin" for a strategy defined in Go.
func FamilyOf(s Strategy) string {
	if f, ok := s.(Familied); ok {
		return f.Family()
	}
	return "builtin"
}

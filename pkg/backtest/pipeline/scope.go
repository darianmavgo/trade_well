package pipeline

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

var safeSymbol = regexp.MustCompile(`^[A-Z0-9.^=_-]+$`)

// cleanSymbols upper-cases, trims and de-duplicates symbols, rejecting any that
// could not be a ticker. The list goes into SQL, so nothing else gets through.
func cleanSymbols(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		if !safeSymbol.MatchString(s) {
			return nil, fmt.Errorf("pipeline: %q is not a ticker symbol", s)
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// strategiesFor returns the ids of every strategy row tied to the symbols in the
// reference database at refDB, by sql/stages/pipeline_scope/01_by_symbol.sql.
func strategiesFor(refDB string, symbols []string) ([]string, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("pipeline: no symbols")
	}
	text, err := sqlfiles.Stages.ReadFile("stages/pipeline_scope/01_by_symbol.sql")
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(symbols))
	for i, s := range symbols {
		quoted[i] = "'" + s + "'"
	}
	q := strings.ReplaceAll(string(text), "__SYMBOL_LIST__", strings.Join(quoted, ","))
	db, err := storage.OpenSQLiteReadOnly(refDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var ids []string
	if err := db.Select(&ids, q); err != nil {
		return nil, fmt.Errorf("pipeline: strategies for %v: %w", symbols, err)
	}
	return ids, nil
}

// checkRegistered returns an error naming every id that no registry resolves.
func checkRegistered(ids []string) error {
	var missing []string
	for _, id := range ids {
		if _, ok := strategy.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("pipeline: not registered: %s", strings.Join(missing, ", "))
	}
	return nil
}

// familyStages maps a -family value to the stage that lists its rows.
var familyStages = map[string]string{"streak": "stages/pipeline_scope/02_family_streak.sql"}

// strategiesForFamily returns the ids of the first limit rows of a strategy
// family (limit <= 0 means every row) and the symbols those rows read or trade,
// by sql/stages/pipeline_scope/02_family_*.sql.
func strategiesForFamily(refDB, family string, limit int) (ids, symbols []string, err error) {
	file, ok := familyStages[family]
	if !ok {
		return nil, nil, fmt.Errorf("pipeline: unknown family %q (families: streak)", family)
	}
	text, err := sqlfiles.Stages.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	n := -1
	if limit > 0 {
		n = limit
	}
	q := strings.ReplaceAll(string(text), "__LIMIT__", fmt.Sprint(n))
	db, err := storage.OpenSQLiteReadOnly(refDB)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	var rows []struct {
		ID           string `db:"id"`
		SignalSymbol string `db:"signal_symbol"`
		TradeSymbol  string `db:"trade_symbol"`
	}
	if err := db.Select(&rows, q); err != nil {
		return nil, nil, fmt.Errorf("pipeline: %s family: %w", family, err)
	}
	var syms []string
	for _, r := range rows {
		ids = append(ids, r.ID)
		syms = append(syms, r.SignalSymbol, r.TradeSymbol)
	}
	symbols, err = cleanSymbols(syms)
	return ids, symbols, err
}

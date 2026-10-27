package runview

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
	"github.com/jmoiron/sqlx"
)

const etfCacheTTL = 10 * time.Minute

type etfRow struct {
	Rank         int     `json:"rank"`
	Symbol       string  `db:"symbol" json:"symbol"`
	Name         string  `db:"name" json:"name"`
	Category     string  `db:"category" json:"category"`
	Leverage     string  `db:"leverage" json:"leverage"`
	DollarVolume float64 `db:"dollar_volume" json:"dollar_volume"`
	Bars         int     `db:"bars" json:"bars"`
	Strategies   int     `json:"strategies"`
	Streak       int     `json:"streak"`
	InRun        int     `json:"in_run"`
}

type symbolStrategy struct {
	ID     string `db:"id" json:"id"`
	Family string `db:"family" json:"family"`
	Name   string `db:"name" json:"name"`
	Symbol string `db:"symbol" json:"-"`
}

// etfCache keeps the ranking, which reads millions of bars, between requests.
type etfCache struct {
	mu   sync.Mutex
	at   time.Time
	key  string
	etfs []etfRow
	pair []symbolStrategy
}

func readStage(name string) (string, error) {
	b, err := sqlfiles.Stages.ReadFile("stages/runview/" + name)
	return string(b), err
}

// attach opens the market database read-only and attaches the universe and
// reference databases, also read-only through the query_only pragma.
func (s *server) attached() (*sqlx.DB, error) {
	db, err := openRO(s.marketDB)
	if err != nil {
		return nil, err
	}
	for alias, path := range map[string]string{"u": s.universeDB, "r": s.refDB} {
		if path == "" {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE 'file:%s?mode=ro' AS %s", path, alias)); err != nil {
			db.Close()
			return nil, fmt.Errorf("attach %s: %w", path, err)
		}
	}
	return db, nil
}

// etfs ranks the largest ETFs by dollar volume and counts the strategies tied
// to each, in the reference database and in the run's scope.
func (s *server) etfs(q url.Values) (any, error) {
	n, _ := strconv.Atoi(q.Get("n"))
	if n <= 0 || n > 500 {
		n = 100
	}
	lev := q.Get("leveraged") != "1"
	key := fmt.Sprintf("%d/%v", n, lev)

	s.etf.mu.Lock()
	defer s.etf.mu.Unlock()
	if s.etf.key != key || time.Since(s.etf.at) > etfCacheTTL {
		if s.marketDB == "" || s.universeDB == "" {
			return nil, fmt.Errorf("the market and universe databases are needed to rank ETFs")
		}
		db, err := s.attached()
		if err != nil {
			return nil, err
		}
		defer db.Close()
		text, err := readStage("01_top_etfs.sql")
		if err != nil {
			return nil, err
		}
		filter := ""
		if lev {
			filter = "AND e.leverage = 'none'"
		}
		text = strings.NewReplacer("__LEVERAGE__", filter, "__LIMIT__", strconv.Itoa(n)).Replace(text)
		var rows []etfRow
		if err := db.Select(&rows, text); err != nil {
			return nil, fmt.Errorf("rank ETFs: %w", err)
		}
		pairText, err := readStage("02_symbol_strategies.sql")
		if err != nil {
			return nil, err
		}
		var pairs []symbolStrategy
		if err := db.Select(&pairs, pairText); err != nil {
			return nil, fmt.Errorf("strategies by symbol: %w", err)
		}
		s.etf.key, s.etf.at, s.etf.etfs, s.etf.pair = key, time.Now(), rows, pairs
	}

	inRun := s.runScope(q.Get("run"))
	bySym := map[string][]symbolStrategy{}
	for _, p := range s.etf.pair {
		bySym[p.Symbol] = append(bySym[p.Symbol], p)
	}
	out := make([]etfRow, len(s.etf.etfs))
	for i, e := range s.etf.etfs {
		e.Rank = i + 1
		for _, p := range bySym[e.Symbol] {
			e.Strategies++
			if p.Family == "streak" {
				e.Streak++
			}
			if inRun[p.ID] {
				e.InRun++
			}
		}
		out[i] = e
	}
	return out, nil
}

// runScope is the set of strategy ids in the run's pipeline scope.
func (s *server) runScope(run string) map[string]bool {
	set := map[string]bool{}
	p, err := s.resolve(run, "pipeline.db")
	if err != nil {
		return set
	}
	db, err := openRO(p)
	if err != nil {
		return set
	}
	defer db.Close()
	var ids []string
	if db.Select(&ids, `SELECT id FROM pipeline_scope WHERE kind = 'strategy'`) == nil {
		for _, id := range ids {
			set[id] = true
		}
	}
	return set
}

// symbol lists the strategies tied to one symbol, with the run's headline
// numbers for each where the run has them.
func (s *server) symbol(q url.Values) (any, error) {
	sym := strings.ToUpper(strings.TrimSpace(q.Get("sym")))
	if !strategyIDRe.MatchString(sym) {
		return nil, fmt.Errorf("bad symbol %q", sym)
	}
	s.etf.mu.Lock()
	empty := len(s.etf.pair) == 0
	s.etf.mu.Unlock()
	if empty { // the ranking call fills the strategy-by-symbol cache
		if _, err := s.etfs(url.Values{"run": q["run"], "n": {"100"}}); err != nil {
			return nil, err
		}
	}
	s.etf.mu.Lock()
	var list []symbolStrategy
	for _, p := range s.etf.pair {
		if p.Symbol == sym {
			list = append(list, p)
		}
	}
	s.etf.mu.Unlock()

	inRun := s.runScope(q.Get("run"))
	perf := s.runPerformance(q.Get("run"))
	type item struct {
		symbolStrategy
		InRun bool           `json:"in_run"`
		Perf  map[string]any `json:"perf,omitempty"`
	}
	out := make([]item, len(list))
	for i, p := range list {
		out[i] = item{symbolStrategy: p, InRun: inRun[p.ID], Perf: perf[p.ID]}
	}
	return out, nil
}

// runPerformance reads the run's own backtest summary, one row per strategy.
func (s *server) runPerformance(run string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, f := range []string{"streak.db", "strategies.db"} {
		p, err := s.resolve(run, f)
		if err != nil {
			continue
		}
		db, err := openRO(p)
		if err != nil {
			continue
		}
		rows, err := db.Queryx(`SELECT strategy_id, cagr, sharpe_ratio, max_drawdown_pct, total_trades, win_rate FROM performance_summary`)
		if err == nil {
			for rows.Next() {
				var id string
				var cagr, sharpe, dd, win *float64
				var trades *int
				if rows.Scan(&id, &cagr, &sharpe, &dd, &trades, &win) == nil {
					if _, seen := out[id]; !seen {
						out[id] = map[string]any{"cagr": cagr, "sharpe": sharpe, "max_dd": dd, "trades": trades, "win_rate": win}
					}
				}
			}
			rows.Close()
		}
		db.Close()
		if len(out) > 0 {
			break
		}
	}
	return out
}

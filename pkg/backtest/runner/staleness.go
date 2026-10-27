package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
	"github.com/olekukonko/tablewriter"
)

// This file answers a different question than coverage.go: not "has this
// strategy been backtested/swept at all" but "is that result still
// trustworthy, or has something changed underneath it since it was
// computed?" Shared by cmd/backtest's and cmd/gridsearch's `stale`
// subcommands so the definition of "stale" doesn't drift between the two.
//
// Three independent signals, each sufficient on its own to call a result
// stale:
//  1. Unregistered — the strategy ID a result belongs to no longer resolves
//     in the current registry (renamed or removed; see runner.ResolveStrategy
//     — exactly what happened to voo-tecl-combo and millwharf this session).
//  2. Data drift — market_history.db now has bars beyond the date the result
//     covers, i.e. there's more history to backtest against than this result
//     saw. Compared by actual trading-day coverage (result's end_date vs. the
//     latest bar date in the DB), not file mtime — cmd/market_history and WAL
//     checkpointing touch the file constantly without necessarily adding a
//     single new row, so an mtime-only check flags almost everything and
//     tells you nothing.
//  3. Pipeline drift — for SQL-pipeline-backed strategies, the newest .sql
//     file in the strategy's pipeline directory was edited after this result
//     was computed, so it may not reflect the current entry/exit logic.
//
// Deliberately NOT attempted: guessing a strategy's source .go file
// from its ID to check code-vs-result mtime. The ID-to-filename convention
// isn't reliable enough across this codebase (e.g. "rsi2" -> rsi2_trend.go)
// to risk a false signal — better to report "n/a" than to lie.

// StaleEntry is one strategy's staleness assessment.
type StaleEntry struct {
	StrategyID   string
	ComputedAt   time.Time // when this analysis was last generated
	Unregistered bool

	ResultEndDate  string // "" if unknown (e.g. no single end date to compare)
	LatestDataDate string // "" if unknown
	DataDaysBehind int    // LatestDataDate - ResultEndDate, in calendar days
	DataStale      bool

	PipelineDir        string
	PipelineModTime    time.Time
	PipelineNewestFile string
	PipelineStale      bool
	HasPipeline        bool
}

// IsStale reports whether any staleness signal fired.
func (e StaleEntry) IsStale() bool {
	return e.Unregistered || e.DataStale || e.PipelineStale
}

// Reasons renders every triggered signal as a human-readable line.
func (e StaleEntry) Reasons() []string {
	var reasons []string
	if e.Unregistered {
		reasons = append(reasons, "strategy no longer registered (renamed or removed)")
	}
	if e.DataStale {
		reasons = append(reasons, fmt.Sprintf("%d more day(s) of market data available (result covers through %s, data now through %s)",
			e.DataDaysBehind, e.ResultEndDate, e.LatestDataDate))
	}
	if e.PipelineStale {
		reasons = append(reasons, fmt.Sprintf("SQL pipeline edited %s after this result (%s)",
			formatAge(e.ComputedAt, e.PipelineModTime), e.PipelineNewestFile))
	}
	return reasons
}

func formatAge(computedAt, changedAt time.Time) string {
	d := changedAt.Sub(computedAt)
	if d < 24*time.Hour {
		return fmt.Sprintf("%.0fh", d.Hours())
	}
	return fmt.Sprintf("%.0fd", d.Hours()/24)
}

// pipelineDirFor returns the directory of .sql scripts backing s, if any —
// either s itself is a *strategy.SQLPipelineStrategy (the auto-registered
// "<dir>-sql" strategies), or it delegates to one via the "<id>-sql" sibling
// convention established by gld-decline/sig-voo-buy-tecl/sig-voo-buy-spxu
// (see pkg/strategy/*.go's GenerateSignals).
func pipelineDirFor(s strategy.Strategy) (string, bool) {
	if sp, ok := s.(*strategy.SQLPipelineStrategy); ok {
		return sp.PipelineDir(), true
	}
	// A row-backed family that names its pipeline directory (the hold family).
	if pd, ok := s.(interface{ PipelineDir() string }); ok {
		return pd.PipelineDir(), true
	}
	siblingID := strings.ReplaceAll(s.ID(), "-", "_") + "-sql"
	if sibling, ok := strategy.Get(siblingID); ok {
		if sp, ok := sibling.(*strategy.SQLPipelineStrategy); ok {
			return sp.PipelineDir(), true
		}
	}
	return "", false
}

// newestSQLFile returns the modification time and path of the most recently
// modified .sql file in dir.
func newestSQLFile(dir string) (time.Time, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, "", err
	}
	var newest time.Time
	var newestPath string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
			newestPath = filepath.Join(dir, entry.Name())
		}
	}
	if newestPath == "" {
		return time.Time{}, "", fmt.Errorf("no .sql files found in %s", dir)
	}
	return newest, newestPath, nil
}

// LatestMarketDate returns the most recent bar date (YYYY-MM-DD) present
// anywhere in market_history.db's backtest_start table. Used only as the
// last-resort fallback in latestDateForStrategy, for a strategy with no
// RequiredSymbols()/Benchmark to narrow the query to.
func LatestMarketDate(marketDB *sqlx.DB) (string, error) {
	var maxDate string
	err := marketDB.Get(&maxDate, `SELECT MAX(substr(Date, 1, 10)) FROM backtest_start`)
	return maxDate, err
}

// latestDateForStrategy returns the most recent bar date (YYYY-MM-DD) among
// s's own required symbols (or its Benchmark, if RequiredSymbols isn't
// implemented). Symbols genuinely differ in how current their data is — e.g.
// MARA/PDD/TSLA lag a day behind VOO in practice — so comparing a strategy's
// result against the wrong symbol's latest date produces a false "stale"
// (or a false "fresh") reading. Falls back to LatestMarketDate only when the
// strategy names no specific symbols at all.
func latestDateForStrategy(marketDB *sqlx.DB, s strategy.Strategy) (string, error) {
	var symbols []string
	if rp, ok := s.(strategy.RequiredSymbolsProvider); ok {
		symbols = rp.RequiredSymbols()
	}
	if len(symbols) == 0 {
		if cfg := s.DefaultConfig(); cfg.Benchmark != "" {
			symbols = []string{cfg.Benchmark}
		}
	}
	if len(symbols) == 0 {
		return LatestMarketDate(marketDB)
	}

	query, args, err := sqlx.In(`SELECT MAX(substr(Date, 1, 10)) FROM backtest_start WHERE symbol IN (?)`, symbols)
	if err != nil {
		return "", err
	}
	query = marketDB.Rebind(query)
	var maxDate string
	err = marketDB.Get(&maxDate, query, args...)
	return maxDate, err
}

// AssessOne checks a single strategy's analysis (computed at computedAt,
// covering data through resultEndDate — a "YYYY-MM-DD" string, "" to skip
// the data-drift check, e.g. a gridsearch sweep with no data_max_date
// recorded yet) against the three staleness signals described above.
// marketDB may be nil to skip the data-drift check entirely (e.g. the market
// DB couldn't be opened).
func AssessOne(strategyID string, computedAt time.Time, resultEndDate string, marketDB *sqlx.DB) StaleEntry {
	e := StaleEntry{
		StrategyID:    strategyID,
		ComputedAt:    computedAt,
		ResultEndDate: resultEndDate,
	}

	s, registered := strategy.Get(strategyID)
	e.Unregistered = !registered
	if !registered {
		return e
	}

	if resultEndDate != "" && marketDB != nil {
		if latestDataDate, err := latestDateForStrategy(marketDB, s); err == nil && latestDataDate != "" {
			e.LatestDataDate = latestDataDate
			rt, rErr := time.Parse("2006-01-02", resultEndDate)
			lt, lErr := time.Parse("2006-01-02", latestDataDate)
			if rErr == nil && lErr == nil && lt.After(rt) {
				e.DataDaysBehind = int(lt.Sub(rt).Hours() / 24)
				e.DataStale = true
			}
		}
	}

	if dir, ok := pipelineDirFor(s); ok {
		e.HasPipeline = true
		e.PipelineDir = dir
		if mt, path, err := newestSQLFile(dir); err == nil {
			e.PipelineModTime = mt
			e.PipelineNewestFile = path
			e.PipelineStale = mt.After(computedAt)
		}
	}

	return e
}

// PrintStalenessReport renders a staleness assessment (see AssessOne) as a
// console table, used identically by `cmd/backtest stale` and
// `cmd/gridsearch stale` so the report reads the same regardless of which
// tool produced it. neverComputed lists currently-registered strategies with
// no result/sweep to assess at all — reported separately since "never run"
// isn't the same claim as "stale."
func PrintStalenessReport(source string, entries []StaleEntry, neverComputed []string) {
	sort.Slice(entries, func(i, j int) bool {
		// Stale first (unregistered, then data, then pipeline), then fresh; alphabetical within each group.
		si, sj := entries[i].IsStale(), entries[j].IsStale()
		if si != sj {
			return si
		}
		return entries[i].StrategyID < entries[j].StrategyID
	})

	fmt.Printf("\n=======================================================================================================================\n")
	fmt.Printf("🕰️  STALENESS ASSESSMENT — %s\n", source)
	fmt.Printf("=======================================================================================================================\n")

	staleCount := 0
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Strategy ID", "Computed At", "Status", "Reasons"})
	table.SetAutoWrapText(false)
	for _, e := range entries {
		status := "✅ fresh"
		reasons := strings.Join(e.Reasons(), "; ")
		if e.IsStale() {
			status = "⚠️  STALE"
			staleCount++
		} else if !e.HasPipeline {
			reasons = "(no sql/strategies pipeline to track; data freshness only)"
		}
		table.Append([]string{e.StrategyID, e.ComputedAt.Format("2006-01-02 15:04"), status, reasons})
	}
	table.Render()

	fmt.Printf("\n%d/%d assessed results are stale.\n", staleCount, len(entries))

	if len(neverComputed) > 0 {
		fmt.Printf("\n📭 %d registered strategies have no result to assess (never run):\n", len(neverComputed))
		PrintMissingList(neverComputed)
	}
	fmt.Println()
}

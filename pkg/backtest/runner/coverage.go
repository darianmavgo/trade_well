package runner

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// This file is the single shared implementation of "has this strategy already
// been backtested, and is that result trustworthy?" — used by every command that
// runs strategies in bulk (cmd/backtest -strategy all, cmd/scoreboard) so that
// skip-duplicate-work logic lives in one place instead of being reinvented (or
// forgotten) per command.

// CompiledResult is the newest finished run of one strategy in results.db.
type CompiledResult struct {
	StrategyID string
	Report     models.PerformanceReport
	DbPath     string
	Increment  int // run_id
	ComputedAt time.Time
	// Engine and Suspect are what the stored run recorded (see storage.RunMeta).
	Engine, Suspect string
}

// LegacyEngineNote is the suspect reason of a stored run that records no engine: it was made before
// runs.engine existed, by the daily simulator, whose same-session fills and exits use the day's range.
const LegacyEngineNote = "made by the daily simulator, before the hourly engine"

// NeedsRedo says a stored run should be run again rather than skipped as already done: it predates
// the hourly engine.
func (c CompiledResult) NeedsRedo() bool { return c.Engine == "" }

// DropStale removes from byStrategy the runs that NeedsRedo, so a bulk command runs those strategies
// again, and returns how many it removed.
func DropStale(byStrategy map[string]CompiledResult) int {
	n := 0
	for id, c := range byStrategy {
		if c.NeedsRedo() {
			delete(byStrategy, id)
			n++
		}
	}
	return n
}

// ScanAndValidate reads the newest finished run of every strategy from the
// family result databases (*.db) of one run directory. This is the one shared
// "what's already been backtested, and can I trust it?" pass every
// bulk-strategy command runs before doing any work. A strategy run is written
// in one transaction, so a crashed run leaves nothing behind to validate; each
// file is integrity-checked once. The remaining return values: files read,
// strategies found, always 0 fallbacks, and how many files failed their check.
// The files are read in parallel, one goroutine per file, up to concurrency.
func ScanAndValidate(runDir string, concurrency int) (byStrategy map[string]CompiledResult, totalFiles, totalGroups, usedFallback, allCompromised int) {
	byStrategy = make(map[string]CompiledResult)
	files, err := storage.ResultFiles(runDir)
	if err != nil || len(files) == 0 {
		return byStrategy, 0, 0, 0, 0
	}
	if concurrency < 1 {
		concurrency = 1
	}
	type scanned struct {
		path string
		runs []storage.LatestRun
		bad  bool
	}
	out := make([]scanned, len(files))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, path := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			family := strings.TrimSuffix(filepath.Base(path), ".db")
			results, err := storage.ResultsFor(runDir, family)
			if err != nil {
				log.Printf("⚠️  cannot open %s: %v", path, err)
				out[i] = scanned{path: path, bad: true}
				return
			}
			if err := results.IntegrityOK(); err != nil {
				log.Printf("⚠️  %s is compromised (%v)", path, err)
				out[i] = scanned{path: path, bad: true}
				return
			}
			runs, err := results.LatestRuns()
			if err != nil {
				log.Printf("⚠️  cannot read runs from %s: %v", path, err)
				out[i] = scanned{path: path, bad: true}
				return
			}
			out[i] = scanned{path: path, runs: runs}
		}(i, path)
	}
	wg.Wait()
	for _, sc := range out {
		if sc.bad {
			allCompromised++
			continue
		}
		for _, r := range sc.runs {
			if r.Engine == "" && r.Suspect == "" {
				r.Suspect = LegacyEngineNote
			}
			byStrategy[r.StrategyID] = CompiledResult{
				StrategyID: r.StrategyID, Report: r.Report, DbPath: sc.path,
				Increment: int(r.RunID), ComputedAt: r.CreatedAt, Engine: r.Engine, Suspect: r.Suspect,
			}
		}
	}
	fmt.Printf("   Found %d strategies with a finished run in %d result databases in %s.\n\n", len(byStrategy), len(files), runDir)
	return byStrategy, len(files), len(byStrategy), 0, allCompromised
}

// MissingStrategies returns the IDs of every currently-registered strategy that
// has no usable (non-compromised) entry in byStrategy — i.e. it has genuinely
// never been backtested, or every run it has was corrupted/incomplete.
func MissingStrategies(byStrategy map[string]CompiledResult) []string {
	var missing []string
	for _, s := range strategy.ListAll() {
		if _, ok := byStrategy[s.ID()]; !ok {
			missing = append(missing, s.ID())
		}
	}
	sort.Strings(missing)
	return missing
}

// ResolveStrategy looks up the live registered strategy for display purposes
// (name/description); falls back to a minimal stub carrying just the ID when a
// result DB refers to a strategy that's no longer registered (renamed/removed).
func ResolveStrategy(id string) strategy.Strategy {
	if s, ok := strategy.Get(id); ok {
		return s
	}
	return &staleStrategy{id: id}
}

type staleStrategy struct{ id string }

func (s *staleStrategy) ID() string                                              { return s.id }
func (s *staleStrategy) Name() string                                            { return s.id + " (not currently registered)" }
func (s *staleStrategy) Description() string                                     { return "" }
func (s *staleStrategy) DefaultConfig() strategy.StrategyConfig                  { return strategy.StrategyConfig{} }
func (s *staleStrategy) Validate() error                                         { return nil }
func (s *staleStrategy) SetDatabases(a, b string)                                {}
func (s *staleStrategy) GenerateSignals(map[string][]models.Bar) []models.Signal { return nil }

// PrintMissingList prints strategy IDs one per line, capped so output against
// hundreds of missing strategies doesn't flood the terminal.
func PrintMissingList(ids []string) {
	const maxShown = 25
	for i, id := range ids {
		if i >= maxShown {
			fmt.Printf("   ... and %d more\n", len(ids)-maxShown)
			break
		}
		fmt.Printf("   - %s\n", id)
	}
}

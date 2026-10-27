package backtest

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// runStackEvalCommand implements `backtest stack-eval`: rank existing strategies
// as idle-cash overlays on one primary, all sharing a single cash ledger.
// No new pkg/strategy types are created — stacking lives in the engine/runner.
func runStackEvalCommand(
	primaryID string,
	explicitSecondaries []string,
	includeUniverse bool,
	stackDepth, concurrency int,
	targetDb, tableName, outDir string,
	capital float64,
	symbolFilter string,
	autoDownload bool,
	downloadYears int,
	persistBest bool,
	override runner.ConfigOverride,
) (stackEvalOutcome, error) {
	if primaryID == "" {
		return stackEvalOutcome{}, fmt.Errorf("stack requires a primary strategy (positional or -primary). Example:\n  ./bin/backtest stack -primary streak-voo-buy-tecl")
	}
	primary, ok := strategy.Get(primaryID)
	if !ok {
		return stackEvalOutcome{}, fmt.Errorf("Primary strategy %q not found. Run ./bin/backtest -strategylist", primaryID)
	}

	// park-<symbol> in the candidate list is the residual book for every run.
	var parkSym string
	{
		var kept []string
		for _, id := range explicitSecondaries {
			if st, ok := strategy.Get(id); ok {
				if rp, isPark := st.(strategy.ResidualProvider); isPark {
					if parkSym != "" && parkSym != rp.ParkSymbol() {
						return stackEvalOutcome{}, fmt.Errorf("stack takes one park, got %s and %s", parkSym, rp.ParkSymbol())
					}
					parkSym = rp.ParkSymbol()
					continue
				}
			}
			kept = append(kept, id)
		}
		if len(kept) == 0 && len(explicitSecondaries) > 0 {
			return stackEvalOutcome{}, fmt.Errorf("stack needs at least one overlay besides the park")
		}
		explicitSecondaries = kept
	}
	cands := runner.OverlayCandidates(primary, runner.OverlayCandidateOptions{
		ExplicitIDs:     explicitSecondaries,
		IncludeUniverse: includeUniverse,
	})
	if len(cands) == 0 {
		return stackEvalOutcome{}, fmt.Errorf("No overlay candidates for %s. Pass -secondary id1,id2 or -include-universe.", primary.ID())
	}

	all := append([]strategy.Strategy{primary}, cands...)
	if err := runner.DetectAndDownloadMissingData(targetDb, tableName, all, symbolFilter, autoDownload, downloadYears); err != nil {
		return stackEvalOutcome{}, fmt.Errorf("Market data resolution error: %v", err)
	}

	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return stackEvalOutcome{}, fmt.Errorf("Failed to open source DB %s: %v", targetDb, err)
	}
	defer db.Close()

	reqSymbols := append(runner.RequiredSymbolsFor(all, symbolFilter), "SPY")
	if parkSym != "" {
		reqSymbols = append(reqSymbols, parkSym)
	}
	// Universe overlays declare no RequiredSymbols — load the full table.
	var fetchSymbols []string
	if includeUniverse && len(explicitSecondaries) == 0 {
		fetchSymbols = nil
		fmt.Printf("\nLoading ALL bars from '%s' (universe overlays enabled)...\n", tableName)
	} else {
		fetchSymbols = reqSymbols
		fmt.Printf("\nLoading bars for %v from '%s' for stack of %s (capital $%.0f)...\n",
			reqSymbols, tableName, primary.ID(), capital)
	}
	barsBySymbol, sortedDates, err := storage.FetchBars(db, tableName, fetchSymbols, backtestStart, backtestEnd)
	if err != nil {
		return stackEvalOutcome{}, fmt.Errorf("Error loading historical bars: %v", err)
	}

	fmt.Printf("Evaluating %d overlay candidates on idle cash of %s...\n", len(cands), primary.ID())
	if override.AllocPct > 0 {
		fmt.Printf("Allocation: %.0f%% of equity per position\n", override.AllocPct*100)
	}
	for _, c := range cands {
		fmt.Printf("  • %s\n", c.ID())
	}

	result := runner.ExecuteStackEval(runner.StackEvalOptions{
		Primary:      primary,
		Candidates:   cands,
		BarsBySymbol: barsBySymbol,
		SortedDates:  sortedDates,
		Capital:      capital,
		OutDir:       outDir,
		MarketDBPath: targetDb,
		Concurrency:  concurrency,
		StackDepth:   stackDepth,
		PersistBest:  persistBest,
		Override:     override,
		DefaultAsset: parkSym,
	})
	runner.PrintStackEvalTearSheet(result)

	out := stackEvalOutcome{DefaultAsset: parkSym}
	if result.BestStack != nil && len(result.BestStackIDs) > 0 {
		out.StackID = result.BestStack.CombinedID
	}
	return out, nil
}

func parseSecondaryList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

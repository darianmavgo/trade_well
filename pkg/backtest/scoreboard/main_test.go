package scoreboard

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
)

// A run that traded through an unadjusted split is left out of the ranking, unless asked for.
func TestRankableLeavesOutSuspectRuns(t *testing.T) {
	runs := []runner.RunResult{
		{Strat: runner.ResolveStrategy("clean")},
		{Strat: runner.ResolveStrategy("split"), Suspect: "unadjusted split? AAA 2026-01-07 x0.50"},
	}
	defer func() { includeSuspect = false }()

	includeSuspect = false
	if got := rankable(runs); len(got) != 1 || got[0].Strat.ID() != "clean" {
		t.Fatalf("ranked %v, want only clean", got)
	}
	includeSuspect = true
	if got := rankable(runs); len(got) != 2 {
		t.Fatalf("ranked %d with -include-suspect, want 2", len(got))
	}
}

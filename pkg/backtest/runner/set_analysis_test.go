package runner

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestAnalyzeSet(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "set.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	set := []SetStrategy{
		{ID: "p", Role: SetPrimary, EntryDates: []string{"2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05"}},
		{ID: "m1", Role: SetMember, EntryDates: []string{"2024-01-04", "2024-01-05", "2024-01-08"}}, // shares 2 of 3
		{ID: "m2", Role: SetMember, EntryDates: []string{"2024-02-01"}},                             // shares none
		{ID: "c_far", Role: SetCandidate, EntryDates: []string{"2024-03-01", "2024-03-04"}},
		{ID: "c_same", Role: SetCandidate, EntryDates: []string{"2024-01-02", "2024-01-03"}},
	}
	trades := []models.Trade{
		{StrategyID: "p", Symbol: "A", NetPnL: 10, ReturnPct: 1, HoldDays: 3},
		{StrategyID: "p", Symbol: "A", NetPnL: -5, ReturnPct: -0.5, HoldDays: 1},
		{StrategyID: "m1", Symbol: "B", NetPnL: 4, ReturnPct: 2, HoldDays: 5},
		{StrategyID: "m1", Symbol: "B", NetPnL: 6, ReturnPct: 3, HoldDays: 5},
	}
	rep, err := AnalyzeSet(db, set, trades)
	if err != nil {
		t.Fatal(err)
	}

	// Members signal on 2024-01-02..05, 01-08 and 02-01: six distinct days. Two of them (01-04, 01-05) have two strategies.
	if rep.SignalDays != 6 || rep.CollisionDays != 2 || rep.MaxPerDay != 2 || rep.Signals != 8 {
		t.Fatalf("days %d collisions %d max %d signals %d", rep.SignalDays, rep.CollisionDays, rep.MaxPerDay, rep.Signals)
	}
	if math.Abs(rep.CollisionPct-2.0/6.0) > 1e-9 {
		t.Fatalf("collision pct %v", rep.CollisionPct)
	}
	if rep.Pairs != 3 || len(rep.Members) != 3 {
		t.Fatalf("member pairs %d", rep.Pairs)
	}
	top := rep.Members[0] // p x m1: 2 shared over the smaller (3 days)
	if top.A != "m1" && top.B != "m1" || top.SharedDays != 2 || math.Abs(top.OverlapPct-2.0/3.0) > 1e-9 || math.Abs(top.Jaccard-2.0/5.0) > 1e-9 {
		t.Fatalf("top pair %+v", top)
	}
	// Candidates: least overlap against the primary first.
	if len(rep.Candidates) != 2 || rep.Candidates[0].B != "c_far" || rep.Candidates[0].SharedDays != 0 ||
		rep.Candidates[1].B != "c_same" || rep.Candidates[1].OverlapPct != 1 {
		t.Fatalf("candidates %+v", rep.Candidates)
	}
	if rep.Trades != 4 || rep.Wins != 3 || rep.WinRate != 0.75 || rep.WorstReturn != -0.5 {
		t.Fatalf("trades %d wins %d win rate %v worst %v", rep.Trades, rep.Wins, rep.WinRate, rep.WorstReturn)
	}
	if len(rep.Stats) != 3 || rep.Stats[0].ID != "p" || rep.Stats[0].Trades != 2 || rep.Stats[0].WinRate != 0.5 {
		t.Fatalf("stats %+v", rep.Stats)
	}
	// m2 has no trades: it still has a row, with zero trades.
	if rep.Stats[2].ID != "m2" || rep.Stats[2].Trades != 0 {
		t.Fatalf("m2 %+v", rep.Stats[2])
	}
}

func TestEntryDates(t *testing.T) {
	got := entryDates([]models.Signal{
		{Date: "2024-01-03 09:30", Entry: 1}, {Date: "2024-01-02", Entry: 1}, {Date: "2024-01-02", Entry: 1},
		{Date: "2024-01-09", Entry: 0}, {Date: "2024-01-10", Entry: -1},
	})
	if len(got) != 2 || got[0] != "2024-01-02" || got[1] != "2024-01-03" {
		t.Fatalf("got %v", got)
	}
}

package options

import (
	"math"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func bar(d string, c float64) models.Bar { return models.Bar{Symbol: "VOO", Date: d, Close: c} }

// One cycle: sell the 105 call on 2026-06-19 (Fri) for 2.00, expiry 2026-07-17.
func chainFor(strike, premium float64) map[string][]storage.OptionChainSeries {
	return map[string][]storage.OptionChainSeries{"2026-07-17": {{
		Contract: models.OptionContract{Ticker: "O:X", Expiry: "2026-07-17", Strike: strike},
		Bars:     []models.OptionBar{{Ticker: "O:X", Date: "2026-06-19", Close: premium}},
	}}}
}

func TestCoveredCall_ExpiresWorthless(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-07-01", 102), bar("2026-07-17", 103)}
	r := SimulateCoveredCall(CoveredCallConfig{Capital: 10000, OTMPct: 5, Commission: 1}, bars, chainFor(105, 2.0))
	if len(r.Trades) != 1 || r.Assigned != 0 {
		t.Fatalf("trades=%d assigned=%d", len(r.Trades), r.Assigned)
	}
	// 100 shares: stock +300, premium 200 - 1 commission, call expires worthless.
	want := 10000 + 300 + 199.0
	if got := r.Equity[len(r.Equity)-1].TotalEquity; math.Abs(got-want) > 1e-6 {
		t.Fatalf("final equity %.2f want %.2f", got, want)
	}
}

func TestCoveredCall_InTheMoneyCapsUpside(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-07-17", 110)}
	r := SimulateCoveredCall(CoveredCallConfig{Capital: 10000, OTMPct: 5}, bars, chainFor(105, 2.0))
	if r.Assigned != 1 {
		t.Fatalf("assigned=%d", r.Assigned)
	}
	// stock +1000, premium +200, pay intrinsic 5*100 → 10000 + 700; buy&hold 11000.
	if got := r.Equity[len(r.Equity)-1].TotalEquity; math.Abs(got-10700) > 1e-6 {
		t.Fatalf("final equity %.2f want 10700", got)
	}
	if got := r.BuyHoldEquity[len(r.BuyHoldEquity)-1].TotalEquity; math.Abs(got-11000) > 1e-6 {
		t.Fatalf("buy&hold %.2f want 11000", got)
	}
}

func TestCoveredCall_NoQuoteHoldsNaked(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-07-17", 110)}
	chains := map[string][]storage.OptionChainSeries{"2026-07-17": {{
		Contract: models.OptionContract{Ticker: "O:X", Expiry: "2026-07-17", Strike: 105},
		Bars:     []models.OptionBar{{Ticker: "O:X", Date: "2026-05-01", Close: 2}}, // stale > 5 days
	}}}
	r := SimulateCoveredCall(CoveredCallConfig{Capital: 10000, OTMPct: 5}, bars, chains)
	if r.Skipped != 1 || len(r.Trades) != 0 {
		t.Fatalf("skipped=%d trades=%d", r.Skipped, len(r.Trades))
	}
}

// Rolls on the prior expiry day, and enters late when the strike first trades after the roll date.
func TestCoveredCall_RollsSameDayAndDelayedEntry(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-06-22", 100), bar("2026-07-17", 101),
		bar("2026-07-20", 101), bar("2026-08-21", 102)}
	chains := map[string][]storage.OptionChainSeries{
		"2026-07-17": {{Contract: models.OptionContract{Ticker: "A", Expiry: "2026-07-17", Strike: 105},
			Bars: []models.OptionBar{{Ticker: "A", Date: "2026-06-22", Close: 1.0}}}}, // first trade after roll
		"2026-08-21": {{Contract: models.OptionContract{Ticker: "B", Expiry: "2026-08-21", Strike: 105},
			Bars: []models.OptionBar{{Ticker: "B", Date: "2026-07-17", Close: 1.5}}}}, // roll day == prior expiry
	}
	r := SimulateCoveredCall(CoveredCallConfig{Capital: 10000, OTMPct: 5}, bars, chains)
	if len(r.Trades) != 2 || r.Skipped != 0 {
		t.Fatalf("trades=%d skipped=%d", len(r.Trades), r.Skipped)
	}
	if r.Trades[0].EntryDate != "2026-06-22" || r.Trades[1].EntryDate != "2026-07-17" {
		t.Fatalf("entries %s, %s", r.Trades[0].EntryDate, r.Trades[1].EntryDate)
	}
}

// Dividends are withdrawn (not reinvested); exercised calls are re-bought with all cash.
func TestCoveredCall_DividendsWithdrawnAndRebuy(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-07-01", 100), bar("2026-07-17", 110)}
	cfg := CoveredCallConfig{Capital: 10000, OTMPct: 5, Dividends: map[string]float64{"2026-07-01": 1.0}}
	r := SimulateCoveredCall(cfg, bars, chainFor(105, 2.0))
	if r.WithdrawnDividends != 100 || r.BuyHoldWithdrawnDividends != 100 {
		t.Fatalf("withdrawn %.2f / bh %.2f, want 100", r.WithdrawnDividends, r.BuyHoldWithdrawnDividends)
	}
	if r.Assigned != 1 || r.Rebuys != 1 {
		t.Fatalf("assigned=%d rebuys=%d", r.Assigned, r.Rebuys)
	}
	// Called away at 105 with 200 premium: cash 10700 buys 97 shares @110 (10670), 30 left; plus 100 withdrawn.
	if got := r.FinalAccountValue; math.Abs(got-10700) > 1e-6 {
		t.Fatalf("account value %.2f want 10700", got)
	}
	if got := r.Equity[len(r.Equity)-1].TotalEquity; math.Abs(got-10800) > 1e-6 {
		t.Fatalf("total equity %.2f want 10800", got)
	}
}

// A strike far from the OTM target is not a substitute: the roll is skipped.
func TestCoveredCall_StrikeTooFarFromTargetSkips(t *testing.T) {
	bars := []models.Bar{bar("2026-06-19", 100), bar("2026-07-17", 100)}
	r := SimulateCoveredCall(CoveredCallConfig{Capital: 10000, OTMPct: 10}, bars, chainFor(105, 2.0)) // 5% strike, 10% target
	if r.Skipped != 1 || len(r.Trades) != 0 {
		t.Fatalf("skipped=%d trades=%d", r.Skipped, len(r.Trades))
	}
}

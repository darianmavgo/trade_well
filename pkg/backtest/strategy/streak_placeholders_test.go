package strategy

import (
	"strings"
	"testing"
)

func TestSubstituteStreakPlaceholdersRejectsInjection(t *testing.T) {
	cfg := StrategyConfig{
		Benchmark:       "TQQQ'; DROP",
		TradeSymbol:     "TECL",
		StreakDirection: "drop",
		Regime:          "All Regimes",
		DeclineDays:     2,
		HoldingWindow:   5,
		TakeProfitPct:   0.03,
		StopLossPct:     0.92,
	}
	got := substitutePlaceholders("WHERE symbol = '__SIGNAL_SYMBOL__'", "id", "f.sql", cfg)
	if !strings.Contains(got, "__SIGNAL_SYMBOL__") || strings.Contains(got, "DROP") {
		t.Fatalf("injection was substituted: %s", got)
	}

	cfg.Benchmark = "VOO"
	cfg.TradeSymbol = ""
	got = substitutePlaceholders("WHERE t.symbol = '__TRADE_SYMBOL__'", "id", "f.sql", cfg)
	if !strings.Contains(got, "__TRADE_SYMBOL__") {
		t.Fatalf("empty trade symbol was substituted: %s", got)
	}

	cfg.TradeSymbol = "TQQQ"
	cfg.StreakDirection = "sideways"
	got = substitutePlaceholders("WHERE v.__STREAK_COL__ >= 2", "id", "f.sql", cfg)
	if !strings.Contains(got, "__STREAK_COL__") {
		t.Fatalf("bad direction was substituted: %s", got)
	}

	cfg.StreakDirection = "rally"
	cfg.Regime = "QQQ>=SMA50"
	got = substitutePlaceholders("AND (__REGIME_PREDICATE__)", "id", "f.sql", cfg)
	if !strings.Contains(got, "__REGIME_PREDICATE__") {
		t.Fatalf("SMA50 regime was substituted: %s", got)
	}

	cfg.Regime = "VOO>=SMA200"
	got = substitutePlaceholders(
		"WHERE symbol = '__SIGNAL_SYMBOL__' AND t.symbol = '__TRADE_SYMBOL__' AND v.__STREAK_COL__ >= 2 AND (__REGIME_PREDICATE__) AND regime = '__REGIME_LABEL__'",
		"id", "f.sql", cfg)
	want := "WHERE symbol = 'VOO' AND t.symbol = 'TQQQ' AND v.up_streak >= 2 AND (v.sma200 <= 0 OR v.close >= v.sma200) AND regime = 'VOO>=SMA200'"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

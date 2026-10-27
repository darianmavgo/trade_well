package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarketSourcesNamesTheHourlyDatabase(t *testing.T) {
	dir := t.TempDir()
	daily := filepath.Join(dir, "market_history.db")
	if err := os.WriteFile(daily, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := MarketSources(daily)
	if len(srcs) != 1 || srcs[0].Path != filepath.Join(dir, "market_history_hourly.db") {
		t.Fatalf("sources = %+v", srcs)
	}
	if srcs[0].Detail != "not found" {
		t.Errorf("detail = %q", srcs[0].Detail)
	}
	var out bytes.Buffer
	PrintMarketSources(&out, daily, "backtest_start")
	if !strings.Contains(out.String(), "market_history_hourly.db") || !strings.Contains(out.String(), "table backtest_start") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestMarketSourcesForStatesWhenHourlyIsNotRead(t *testing.T) {
	daily := filepath.Join(t.TempDir(), "market_history.db")
	var out bytes.Buffer
	PrintMarketSourcesFor(&out, daily, "backtest_start", false)
	if !strings.Contains(out.String(), "not read for fills here") {
		t.Errorf("output:\n%s", out.String())
	}
	if got := MarketSourcesFor(daily, true)[0].Use; strings.Contains(got, "not read") {
		t.Errorf("hourly use = %q", got)
	}
}

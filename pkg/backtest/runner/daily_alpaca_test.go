package runner

import (
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestFinishedDailyDropsOpenSession(t *testing.T) {
	bars := []models.Bar{
		{Symbol: "spy", Date: "2026-10-12", Close: 1},
		{Symbol: "spy", Date: "2026-10-13", Close: 2},
		{Symbol: "spy", Date: "2026-10-14", Close: 3},
	}
	// 2026-10-13 18:00 ET: the 13th has closed, the 14th has not begun.
	now := time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC)
	got := FinishedDaily(bars, now)
	if len(got) != 2 || got[1].Date != "2026-10-13" || got[1].Symbol != "SPY" || got[1].Timeframe != "1d" {
		t.Fatalf("got %+v", got)
	}
	// 2026-10-13 15:00 ET: the 13th is still open.
	got = FinishedDaily(bars, time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC))
	if len(got) != 1 || got[0].Date != "2026-10-12" {
		t.Fatalf("open session kept: %+v", got)
	}
}

package hoursim

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func replayCfg() strategy.StrategyConfig {
	return strategy.StrategyConfig{
		NextDayLimitEntry: true,
		HoldingWindow:     1,
		TakeProfitPct:     10, // keep the target out of reach
		StopLossPct:       0.5,
	}
}

func TestReplayRoundTripFillsNextSessionAndExitsOnTime(t *testing.T) {
	sig := models.Signal{Symbol: "TSLL", Date: "2026-09-24", Close: 10, BuyLimit: 10}
	daily := []models.Bar{
		dbar("2026-09-24", 10, 10.2, 9.8, 10),
		dbar("2026-09-25", 10, 10.2, 9.5, 10.1),
		dbar("2026-09-28", 10.1, 10.3, 10, 10.2),
	}
	got := ReplayRoundTrip(sig, daily, nil, replayCfg())
	if got.Outcome != RoundTripClosed {
		t.Fatalf("outcome %s", got.Outcome)
	}
	if got.Trade.EntryDate != "2026-09-25" || got.Trade.EntryPrice != 10 || got.Trade.Shares != 1 {
		t.Fatalf("entry %s @ %v x%d", got.Trade.EntryDate, got.Trade.EntryPrice, got.Trade.Shares)
	}
	if got.Trade.ExitDate != "2026-09-28" || got.Trade.ExitReason != models.ExitReasonTimeUp {
		t.Fatalf("exit %s %s", got.Trade.ExitDate, got.Trade.ExitReason)
	}
}

func TestReplayRoundTripUnfilledWhenLimitNeverTrades(t *testing.T) {
	sig := models.Signal{Symbol: "TSLL", Date: "2026-09-24", Close: 10, BuyLimit: 10}
	daily := []models.Bar{dbar("2026-09-24", 10, 10, 10, 10), dbar("2026-09-25", 11, 12, 10.5, 11.5)}
	got := ReplayRoundTrip(sig, daily, nil, replayCfg())
	if got.Outcome != RoundTripUnfilled {
		t.Fatalf("outcome %s, trade %+v", got.Outcome, got.Trade)
	}
}

func TestReplayRoundTripFillsAtTheOpenWhenGappedThrough(t *testing.T) {
	sig := models.Signal{Symbol: "TSLL", Date: "2026-09-24", Close: 10, BuyLimit: 10}
	daily := []models.Bar{
		dbar("2026-09-24", 10, 10, 10, 10),
		dbar("2026-09-25", 9, 9.5, 8.8, 9.2),
		dbar("2026-09-28", 9.2, 9.4, 9, 9.3),
	}
	got := ReplayRoundTrip(sig, daily, nil, replayCfg())
	if got.Outcome != RoundTripClosed || got.Trade.EntryPrice != 9 {
		t.Fatalf("got %+v", got)
	}
}

// The entry fills on the last bar there is: the position is open, not unfilled.
func TestReplayRoundTripStaysOpenWhenBarsEnd(t *testing.T) {
	sig := models.Signal{Symbol: "TSLL", Date: "2026-09-24", Close: 10, BuyLimit: 10}
	daily := []models.Bar{dbar("2026-09-24", 10, 10, 10, 10), dbar("2026-09-25", 10, 10.2, 9.5, 10.1)}
	got := ReplayRoundTrip(sig, daily, nil, replayCfg())
	if got.Outcome != RoundTripOpen || got.Trade.EntryPrice != 10 {
		t.Fatalf("got %+v", got)
	}
}

// One more session after the fill and the position is still open when the data ends.
func TestReplayRoundTripOpenAfterALaterSession(t *testing.T) {
	cfg := replayCfg()
	cfg.HoldingWindow = 5
	sig := models.Signal{Symbol: "TSLL", Date: "2026-09-24", Close: 10, BuyLimit: 10}
	daily := []models.Bar{
		dbar("2026-09-24", 10, 10, 10, 10),
		dbar("2026-09-25", 10, 10.2, 9.5, 10.1),
		dbar("2026-09-28", 10.1, 10.3, 10, 10.2),
	}
	got := ReplayRoundTrip(sig, daily, nil, cfg)
	if got.Outcome != RoundTripOpen || got.Trade.EntryDate != "2026-09-25" {
		t.Fatalf("got %+v", got)
	}
}

func TestReplayRoundTripNoBars(t *testing.T) {
	if got := ReplayRoundTrip(models.Signal{Symbol: "TSLL", Date: "2026-09-24"}, nil, nil, replayCfg()); got.Outcome != RoundTripNoBars {
		t.Fatalf("outcome %s", got.Outcome)
	}
}

// With hourly bars the replay sees what a daily bar cannot: the target was touched at 9:00, the
// limit only filled at 11:00, so the target is not booked on the entry session.
func TestReplayRoundTripUsesHourlyBarsWhenGiven(t *testing.T) {
	cfg := replayCfg()
	cfg.TakeProfitPct = 0.03
	cfg.HoldingWindow = 1
	sig := models.Signal{Symbol: "QLD", Date: "2026-03-02", Close: 100, BuyLimit: 100}
	daily := []models.Bar{
		dbar("2026-03-02", 100, 101, 99, 100),
		dbar("2026-03-03", 103, 104, 96, 97),
		dbar("2026-03-04", 97, 98, 96, 97),
	}
	hourly := []models.Bar{
		hb("2026-03-02 09:00", 100, 101, 99, 100), hb("2026-03-02 15:00", 100, 100, 99, 100),
		hb("2026-03-03 09:00", 103, 104, 103, 103), // the 3% target (103) is touched here, before the fill
		hb("2026-03-03 11:00", 101, 101, 96, 97),   // the 100 limit fills here
		hb("2026-03-03 15:00", 97, 97, 96, 97),
		hb("2026-03-04 09:00", 97, 98, 96, 97), hb("2026-03-04 15:00", 97, 97, 96, 97),
	}
	got := ReplayRoundTrip(sig, daily, hourly, cfg)
	if got.Outcome != RoundTripClosed || got.Trade.ExitReason == models.ExitReasonProfitTarget {
		t.Fatalf("got %+v, want a closed trade that did not take the earlier target", got)
	}
	if got.Trade.EntryPrice != 100 {
		t.Errorf("entry %v, want the 100 limit", got.Trade.EntryPrice)
	}
}

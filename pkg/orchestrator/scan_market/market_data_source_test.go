package scan_market

import (
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

func TestDailyBarsDropsUnfinishedSession(t *testing.T) {
	et, _ := time.LoadLocation("America/New_York")
	ms := func(d string) int64 {
		tm, _ := time.Parse("2006-01-02 15:04", d+" 06:00")
		return tm.UnixMilli()
	}
	candles := []schwab.Candle{
		{Open: 1, High: 2, Low: 1, Close: 2, Volume: 10, Datetime: ms("2026-10-09")},
		{Open: 2, High: 3, Low: 2, Close: 3, Volume: 20, Datetime: ms("2026-10-12")},
		{Open: 0, High: 3, Low: 2, Close: 3, Datetime: ms("2026-10-08")},
	}
	pre := dailyBars("vrtl", candles, time.Date(2026, 10, 12, 9, 28, 0, 0, et))
	if len(pre) != 1 || pre[0].Date != "2026-10-09" || pre[0].Symbol != "VRTL" || pre[0].Timeframe != "1d" {
		t.Fatalf("premarket: %+v", pre)
	}
	post := dailyBars("vrtl", candles, time.Date(2026, 10, 12, 16, 30, 0, 0, et))
	if len(post) != 2 || post[1].Date != "2026-10-12" {
		t.Fatalf("after close: %+v", post)
	}
}

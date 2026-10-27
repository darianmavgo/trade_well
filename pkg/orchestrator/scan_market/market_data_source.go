package scan_market

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// MarketDataSource says where a live scan refreshes daily bars from:
// MARKET_DATA_SOURCE=alpaca_daily|schwab|alpaca, and when unset alpaca_daily on a
// deployment (App Engine) and alpaca locally. alpaca is the generous
// hourly history for backtests. alpaca_daily is Alpaca's daily bars, which are there
// as soon as a session closes. schwab daily history lags a night behind (at 20:25 ET
// it still ended on the previous session), so it cannot feed an evening scan.
func MarketDataSource() string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_DATA_SOURCE"))); v {
	case "schwab", "alpaca", "alpaca_daily":
		return v
	}
	if runtimeenv.Detect().Kind != runtimeenv.Local {
		return "alpaca_daily"
	}
	return "alpaca"
}

// useSchwabDailyBars points the livescan downloader at Schwab's daily price history
// when MarketDataSource is "schwab". The returned func restores the default.
func useSchwabDailyBars(ctx context.Context, cfg *appconfig.Config) (restore func(), source string, err error) {
	switch MarketDataSource() {
	case "alpaca_daily":
		prev := runner.DailyBarSource
		runner.DailyBarSource = runner.AlpacaDailyBars
		return func() { runner.DailyBarSource = prev }, "alpaca_daily", nil
	case "alpaca":
		return func() {}, "alpaca", nil
	}
	if cfg == nil || cfg.Schwab == nil {
		return nil, "", fmt.Errorf("MARKET_DATA_SOURCE=schwab needs the Schwab config")
	}
	tc, err := trader.NewTraderClient(ctx, cfg.Schwab)
	if err != nil {
		return nil, "", fmt.Errorf("schwab market data: %w", err)
	}
	prev := runner.DailyBarSource
	runner.DailyBarSource = func(ctx context.Context, symbol string, years int) ([]models.Bar, error) {
		candles, err := tc.Client.GetDailyHistory(ctx, symbol, years)
		if err != nil {
			return nil, err
		}
		return dailyBars(symbol, candles, time.Now()), nil
	}
	return func() { runner.DailyBarSource = prev }, "schwab", nil
}

// dailyBars turns Schwab candles into 1d session bars. The candle of a session that
// has not closed yet is dropped: a provisional bar must not be read as a finished
// session (the tip decides which session the entries target).
func dailyBars(symbol string, candles []schwab.Candle, now time.Time) []models.Bar {
	et, _ := time.LoadLocation("America/New_York")
	nowET := now.In(et)
	today := nowET.Format("2006-01-02")
	closed := nowET.Hour() > 16 || (nowET.Hour() == 16 && nowET.Minute() >= 15)
	var out []models.Bar
	for _, c := range candles {
		if c.Open <= 0 || c.High <= 0 || c.Low <= 0 || c.Close <= 0 {
			continue
		}
		// Daily candles are stamped at midnight Central; the UTC date is the session.
		day := time.UnixMilli(c.Datetime).UTC().Format("2006-01-02")
		if day == today && !closed {
			continue
		}
		out = append(out, models.Bar{
			Symbol: strings.ToUpper(symbol), Date: day, Timeframe: "1d", AssetClass: "equity",
			Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, AdjClose: c.Close, Volume: c.Volume,
		})
	}
	return out
}

// withDataSource names the market data source in a failed scan's error, so a stale tip reads as
// "source X only had bars through D" and not as an anonymous refresh failure.
func withDataSource(source string, err error) error {
	return fmt.Errorf("market data source %q: %w", source, err)
}

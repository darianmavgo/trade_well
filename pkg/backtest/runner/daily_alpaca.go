package runner

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// AlpacaDailyBars is a DailyBarSource that reads Alpaca's own daily bars (timeframe 1Day,
// split-adjusted). Alpaca has a session's bar as soon as it closes, where Schwab's daily history
// lags a night behind, so a deployment with no hourly data can still decide on the session that
// just ended. A bar for a session that has not closed is dropped.
func AlpacaDailyBars(ctx context.Context, symbol string, years int) ([]models.Bar, error) {
	src := datasource.NewAlpacaDataSource("", "", &http.Client{Timeout: 60 * time.Second})
	now := time.Now().UTC()
	bars, err := src.Fetch(ctx, datasource.FetchRequest{
		Symbol: symbol, StartDate: now.AddDate(-years, 0, 0), EndDate: now, Timeframe: "1d",
	})
	if err != nil {
		return nil, err
	}
	return FinishedDaily(bars, now), nil
}

// FinishedDaily keeps the daily bars dated at or before the last completed session as of now, and
// stamps them as equity 1d rows.
func FinishedDaily(bars []models.Bar, now time.Time) []models.Bar {
	last := LastCompletedEquitySession(now).Format("2006-01-02")
	out := make([]models.Bar, 0, len(bars))
	for _, b := range bars {
		if b.Date > last {
			continue
		}
		b.Symbol = strings.ToUpper(b.Symbol)
		b.Timeframe = "1d"
		b.AssetClass = "equity"
		out = append(out, b)
	}
	return out
}

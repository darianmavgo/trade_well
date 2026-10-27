package schwab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Candle is one price-history bar. Datetime is epoch milliseconds.
type Candle struct {
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   int64   `json:"volume"`
	Datetime int64   `json:"datetime"`
}

type priceHistory struct {
	Symbol  string   `json:"symbol"`
	Empty   bool     `json:"empty"`
	Candles []Candle `json:"candles"`
}

// historyYears are the only `period` values Schwab accepts with periodType=year.
var historyYears = []int{1, 2, 3, 5, 10, 15, 20}

// PeriodYears rounds years up to a period Schwab accepts (1, 2, 3, 5, 10, 15, 20), so asking
// for 4 or 6 years returns 5 or 10 instead of a 400. Callers trim to the span they need.
func PeriodYears(years int) int {
	for _, y := range historyYears {
		if years <= y {
			return y
		}
	}
	return historyYears[len(historyYears)-1]
}

// GetDailyHistory returns about the last `years` years of daily candles for symbol,
// rounded up to a period Schwab accepts (see PeriodYears),
// oldest first. Regular-session data only. Schwab's daily candle for the current
// session may be provisional until the close; callers decide whether to keep it.
func (c *Client) GetDailyHistory(ctx context.Context, symbol string, years int) ([]Candle, error) {
	years = PeriodYears(years)
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(strings.TrimSpace(symbol)))
	q.Set("periodType", "year")
	q.Set("period", fmt.Sprintf("%d", years))
	q.Set("frequencyType", "daily")
	q.Set("frequency", "1")
	q.Set("needExtendedHoursData", "false")
	urlStr := "https://api.schwabapi.com/marketdata/v1/pricehistory?" + q.Encode()

	var out priceHistory
	if err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &out); err != nil {
		return nil, err
	}
	if out.Empty || len(out.Candles) == 0 {
		return nil, fmt.Errorf("no price history for %s", symbol)
	}
	return out.Candles, nil
}

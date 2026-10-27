package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

type YFResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
				Adjclose []struct {
					Adjclose []*float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// YahooDataSource fetches historical market data using the Yahoo Finance Chart v8 API.
type YahooDataSource struct {
	HTTPClient *http.Client
}

// NewYahooDataSource creates a new Yahoo Finance data source with standard timeouts.
func NewYahooDataSource(client *http.Client) *YahooDataSource {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &YahooDataSource{
		HTTPClient: client,
	}
}

func (y *YahooDataSource) Name() string {
	return "yahoo"
}

func (y *YahooDataSource) Fetch(ctx context.Context, req FetchRequest) ([]models.Bar, error) {
	interval := "1d"
	if req.Timeframe != "" {
		interval = req.Timeframe
	}

	startUnix := req.StartDate.Unix()
	endUnix := req.EndDate.Unix()
	if endUnix <= 0 {
		endUnix = time.Now().Unix()
	}

	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=%s&events=history",
		req.Symbol, startUnix, endUnix, interval,
	)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := y.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("yahoo request failed for %s: %w", req.Symbol, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo API HTTP %d for %s", resp.StatusCode, req.Symbol)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read yahoo response: %w", err)
	}

	var yf YFResponse
	if err := json.Unmarshal(body, &yf); err != nil {
		return nil, fmt.Errorf("failed to decode yahoo json: %w", err)
	}

	if len(yf.Chart.Result) == 0 {
		return nil, fmt.Errorf("no chart result returned for %s", req.Symbol)
	}

	res := yf.Chart.Result[0]
	if len(res.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("no quote indicators returned for %s", req.Symbol)
	}

	quotes := res.Indicators.Quote[0]
	timestamps := res.Timestamp

	var bars []models.Bar
	for i, ts := range timestamps {
		if i >= len(quotes.Open) || quotes.Open[i] == nil {
			continue
		}
		if quotes.Close[i] == nil {
			// Yahoo leaves the just-closed session's close (and adjclose) null for
			// a while but carries open/high/low/volume. Without this the bar is
			// dropped and the market DB tip stays a session behind.
			c, ok := settledCloseFromMeta(i == len(timestamps)-1, ts, quotes.High[i], quotes.Low[i], res.Meta.RegularMarketPrice, res.Meta.RegularMarketTime)
			if !ok {
				continue
			}
			log.Printf("[yahoo] %s %s: close is null; using settled regularMarketPrice %.4f (a later refresh replaces it with Yahoo's own close)",
				req.Symbol, time.Unix(ts, 0).UTC().Format("2006-01-02"), c)
			quotes.Close[i] = &c
		}
		t := time.Unix(ts, 0).UTC()
		dateStr := t.Format("2006-01-02")

		o := *quotes.Open[i]
		h := *quotes.High[i]
		l := *quotes.Low[i]
		c := *quotes.Close[i]

		var v int64
		if i < len(quotes.Volume) && quotes.Volume[i] != nil {
			v = *quotes.Volume[i]
		}

		adj := c
		if len(res.Indicators.Adjclose) > 0 && len(res.Indicators.Adjclose[0].Adjclose) > i {
			if res.Indicators.Adjclose[0].Adjclose[i] != nil {
				adj = *res.Indicators.Adjclose[0].Adjclose[i]
			}
		}

		bars = append(bars, models.Bar{
			Idx:        len(bars) + 1,
			Symbol:     req.Symbol,
			Date:       dateStr,
			Timeframe:  interval,
			AssetClass: "equity",
			Open:       o,
			High:       h,
			Low:        l,
			Close:      c,
			AdjClose:   adj,
			Volume:     v,
			ParsedDt:   t,
		})
	}

	return bars, nil
}

// settledCloseFromMeta returns the regular-session close for a daily bar whose
// close Yahoo left null, taken from the response's regularMarketPrice. It only
// does so when that price is safe to treat as the official close:
//   - the bar is the last one returned (an older bar with a null close is a real
//     data gap, not a pending close);
//   - regularMarketTime is on the bar's own America/New_York date and at or after
//     the 16:00 ET close, so an in-progress session never yields a partial bar;
//   - the price is positive and inside the bar's own high/low range.
//
// Early-close days (13:00 ET) fail the 16:00 test and are left to Yahoo.
func settledCloseFromMeta(isLast bool, barTS int64, high, low *float64, price float64, marketTS int64) (float64, bool) {
	if !isLast || price <= 0 || marketTS <= 0 || high == nil || low == nil {
		return 0, false
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0, false
	}
	bar := time.Unix(barTS, 0).In(loc)
	mkt := time.Unix(marketTS, 0).In(loc)
	if bar.Format("2006-01-02") != mkt.Format("2006-01-02") || mkt.Hour() < 16 {
		return 0, false
	}
	if price < *low || price > *high {
		return 0, false
	}
	return price, true
}

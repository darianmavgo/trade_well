package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// AlpacaDataSource implements DataSource for the Alpaca Market Data v2 stock
// bars API. ALPACA_ENDPOINT in .env is the trading API; bars come from the
// separate data host below.
type AlpacaDataSource struct {
	KeyID      string
	Secret     string
	BaseURL    string
	HTTPClient *http.Client

	// RetryWait is how long to sleep after an HTTP 429 (default 20s). A 429 is
	// retried up to 5 times.
	RetryWait time.Duration
}

type alpacaBarsResponse struct {
	Bars []struct {
		T string  `json:"t"`
		O float64 `json:"o"`
		H float64 `json:"h"`
		L float64 `json:"l"`
		C float64 `json:"c"`
		V float64 `json:"v"`
	} `json:"bars"`
	NextPageToken string `json:"next_page_token"`
}

// alpacaFeed is the Alpaca data feed every request asks for: sip, the consolidated tape (it needs a
// paid Alpaca data plan). It is not configurable.
const alpacaFeed = "sip"

// NewAlpacaDataSource builds the source. Empty keyID/secret fall back to ALPACA_KEY / ALPACA_SECRET.
func NewAlpacaDataSource(keyID, secret string, client *http.Client) *AlpacaDataSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if strings.TrimSpace(keyID) == "" || strings.TrimSpace(secret) == "" {
		keyID, secret = ResolveAlpacaKeys()
	}
	return &AlpacaDataSource{
		KeyID: strings.TrimSpace(keyID), Secret: strings.TrimSpace(secret),
		BaseURL: "https://data.alpaca.markets", HTTPClient: client,
	}
}

// ResolveAlpacaKeys returns ALPACA_KEY and ALPACA_SECRET from the environment
// or the project .env file (see pkg/appenv).
func ResolveAlpacaKeys() (keyID, secret string) {
	return appenv.Get("ALPACA_KEY"), appenv.Get("ALPACA_SECRET")
}

// Name returns the provider identifier "alpaca".
func (a *AlpacaDataSource) Name() string { return "alpaca" }

// alpacaTimeframe converts "1h", "5m", "1d" to Alpaca's "1Hour", "5Min", "1Day".
func alpacaTimeframe(tf string) string {
	n, span := parsePolygonTimeframe(tf)
	switch span {
	case "minute":
		return fmt.Sprintf("%dMin", n)
	case "hour":
		return fmt.Sprintf("%dHour", n)
	case "week":
		return "1Week"
	case "month":
		return "1Month"
	}
	return "1Day"
}

// Fetch retrieves split-adjusted bars from Alpaca. Intraday rows are stamped
// in UTC ("2006-01-02 15:04:05"), the same as the Polygon source.
func (a *AlpacaDataSource) Fetch(ctx context.Context, req FetchRequest) ([]models.Bar, error) {
	if a.KeyID == "" || a.Secret == "" {
		return nil, fmt.Errorf("alpaca keys are missing. Set ALPACA_KEY and ALPACA_SECRET in environment or .env file")
	}
	sym := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if sym == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}
	now := time.Now().UTC()
	start, end := req.StartDate, req.EndDate
	if start.IsZero() {
		start = now.AddDate(-4, 0, 0)
	}
	if end.IsZero() {
		end = now
	}
	if !end.After(start) || end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 {
		end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, time.UTC)
	}
	// Alpaca refuses an end inside the last 15 minutes on the free feed.
	if limit := now.Add(-16 * time.Minute); end.After(limit) {
		end = limit
	}

	tf := alpacaTimeframe(req.Timeframe)
	intraday := strings.HasSuffix(tf, "Min") || strings.HasSuffix(tf, "Hour")
	intervalName := req.Timeframe
	if intervalName == "" {
		intervalName = "1d"
	}

	var bars []models.Bar
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("timeframe", tf)
		q.Set("start", start.UTC().Format(time.RFC3339))
		q.Set("end", end.UTC().Format(time.RFC3339))
		q.Set("adjustment", "split")
		q.Set("feed", alpacaFeed)
		q.Set("sort", "asc")
		q.Set("limit", "10000")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		u := fmt.Sprintf("%s/v2/stocks/%s/bars?%s", a.BaseURL, url.PathEscape(sym), q.Encode())

		var body []byte
		for attempt := 0; ; attempt++ {
			httpReq, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to build alpaca request: %w", err)
			}
			httpReq.Header.Set("APCA-API-KEY-ID", a.KeyID)
			httpReq.Header.Set("APCA-API-SECRET-KEY", a.Secret)
			httpReq.Header.Set("User-Agent", "BacktestGoSQLite/1.0")
			resp, err := a.HTTPClient.Do(httpReq)
			if err != nil {
				return nil, fmt.Errorf("alpaca request failed for %s: %w", sym, err)
			}
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to read alpaca response body: %w", err)
			}
			if resp.StatusCode == http.StatusTooManyRequests && attempt < 5 {
				pause := a.RetryWait
				if pause <= 0 {
					pause = 20 * time.Second
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(pause):
				}
				continue
			}
			switch resp.StatusCode {
			case http.StatusOK:
			case http.StatusUnauthorized:
				return nil, fmt.Errorf("alpaca authentication failed (HTTP 401): check ALPACA_KEY / ALPACA_SECRET")
			case http.StatusForbidden:
				return nil, fmt.Errorf("alpaca access forbidden (HTTP 403) for %s on feed %q: %s", sym, alpacaFeed, string(body))
			default:
				return nil, fmt.Errorf("alpaca API HTTP %d for %s: %s", resp.StatusCode, sym, string(body))
			}
			break
		}

		var page alpacaBarsResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("failed to decode alpaca json response: %w", err)
		}
		for _, r := range page.Bars {
			t, err := time.Parse(time.RFC3339, r.T)
			if err != nil {
				return nil, fmt.Errorf("bad alpaca timestamp %q: %w", r.T, err)
			}
			t = t.UTC()
			dateStr := t.Format("2006-01-02")
			if intraday {
				dateStr = t.Format("2006-01-02 15:04:05")
			}
			bars = append(bars, models.Bar{
				Idx: len(bars) + 1, Symbol: sym, Date: dateStr, Timeframe: intervalName,
				AssetClass: "equity",
				Open:       r.O, High: r.H, Low: r.L, Close: r.C, AdjClose: r.C,
				Volume: int64(r.V), ParsedDt: t,
			})
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no alpaca bars returned for %s between %s and %s", sym, start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	return bars, nil
}

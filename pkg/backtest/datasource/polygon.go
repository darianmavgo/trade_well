package datasource

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// PolygonAggsResponse represents the Polygon.io Aggregates (Bars) v2 API response payload.
type PolygonAggsResponse struct {
	Ticker       string             `json:"ticker"`
	QueryCount   int                `json:"queryCount"`
	ResultsCount int                `json:"resultsCount"`
	Adjusted     bool               `json:"adjusted"`
	Status       string             `json:"status"`
	RequestID    string             `json:"request_id"`
	Count        int                `json:"count"`
	NextURL      string             `json:"next_url"`
	Message      string             `json:"message"`
	Error        string             `json:"error"`
	Results      []PolygonAggResult `json:"results"`
}

// PolygonAggResult represents a single aggregate candlestick bar.
type PolygonAggResult struct {
	V  float64 `json:"v"`  // Volume
	VW float64 `json:"vw"` // Volume weighted average price
	O  float64 `json:"o"`  // Open price
	C  float64 `json:"c"`  // Close price
	H  float64 `json:"h"`  // High price
	L  float64 `json:"l"`  // Low price
	T  int64   `json:"t"`  // Unix timestamp (milliseconds)
	N  int64   `json:"n"`  // Number of transactions
}

// PolygonDataSource implements DataSource for the Polygon.io API.
type PolygonDataSource struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client

	// MinInterval spaces every request (pages included) at least this far
	// apart; zero means no spacing. 12s keeps the free tier's 5 calls a minute.
	MinInterval time.Duration
	// RetryWait is how long to sleep after an HTTP 429 before asking again
	// (default 65s, a full minute window). A 429 is retried up to 5 times.
	RetryWait time.Duration

	mu   sync.Mutex
	last time.Time
}

// SetRate spaces requests to at most perMinute calls a minute (0 = unthrottled).
func (p *PolygonDataSource) SetRate(perMinute int) {
	if perMinute <= 0 {
		p.MinInterval = 0
		return
	}
	p.MinInterval = time.Minute / time.Duration(perMinute)
}

// wait blocks until the next request may go out.
func (p *PolygonDataSource) wait(ctx context.Context) error {
	if p.MinInterval <= 0 {
		return ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := p.MinInterval - time.Since(p.last); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	p.last = time.Now()
	return nil
}

// NewPolygonDataSource creates a Polygon.io data source.
// If apiKey is empty, it attempts to read from the POLYGON_API_KEY environment variable
// or from a .env file located in the workspace root or parent directories.
func NewPolygonDataSource(apiKey string, client *http.Client) *PolygonDataSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = ResolvePolygonAPIKey()
	}

	return &PolygonDataSource{
		APIKey:     key,
		BaseURL:    "https://api.polygon.io",
		HTTPClient: client,
	}
}

// Name returns the provider identifier "polygon".
func (p *PolygonDataSource) Name() string {
	return "polygon"
}

// Fetch retrieves historical OHLCV bars from Polygon.io.
func (p *PolygonDataSource) Fetch(ctx context.Context, req FetchRequest) ([]models.Bar, error) {
	if p.APIKey == "" {
		return nil, fmt.Errorf("polygon API key is missing. Set POLYGON_API_KEY in environment or .env file, or pass -polygon-key")
	}

	sym := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if sym == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}

	multiplier, timespan := parsePolygonTimeframe(req.Timeframe)

	now := time.Now().UTC()
	start := req.StartDate
	end := req.EndDate
	if start.IsZero() {
		start = now.AddDate(-4, 0, 0)
	}
	if end.IsZero() {
		end = now
	}

	fromStr := start.Format("2006-01-02")
	toStr := end.Format("2006-01-02")

	// Initial endpoint
	endpointURL := fmt.Sprintf(
		"%s/v2/aggs/ticker/%s/range/%d/%s/%s/%s?adjusted=true&sort=asc&limit=50000",
		p.BaseURL, url.PathEscape(sym), multiplier, timespan, fromStr, toStr,
	)

	var allResults []PolygonAggResult
	currentURL := endpointURL

	for currentURL != "" {
		var body []byte
		for attempt := 0; ; attempt++ {
			if err := p.wait(ctx); err != nil {
				return nil, err
			}
			httpReq, err := http.NewRequestWithContext(ctx, "GET", currentURL, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to build polygon request: %w", err)
			}

			httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
			httpReq.Header.Set("User-Agent", "BacktestGoSQLite/1.0")

			resp, err := p.HTTPClient.Do(httpReq)
			if err != nil {
				return nil, fmt.Errorf("polygon request failed for %s: %w", sym, err)
			}

			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to read polygon response body: %w", err)
			}

			if resp.StatusCode == http.StatusTooManyRequests && p.MinInterval > 0 && attempt < 5 {
				pause := p.RetryWait
				if pause <= 0 {
					pause = 65 * time.Second
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
				// Success, proceed to parse
			case http.StatusUnauthorized:
				return nil, fmt.Errorf("polygon API authentication failed (HTTP 401): invalid or unauthorized POLYGON_API_KEY")
			case http.StatusForbidden:
				return nil, fmt.Errorf("polygon API access forbidden (HTTP 403): check plan entitlements for %s or timeframe", sym)
			case http.StatusTooManyRequests:
				return nil, fmt.Errorf("polygon API rate limit exceeded (HTTP 429): free tier limits to 5 requests per minute")
			default:
				return nil, fmt.Errorf("polygon API HTTP %d for %s: %s", resp.StatusCode, sym, string(body))
			}
			break
		}

		var aggsResp PolygonAggsResponse
		if err := json.Unmarshal(body, &aggsResp); err != nil {
			return nil, fmt.Errorf("failed to decode polygon json response: %w", err)
		}

		if len(aggsResp.Results) > 0 {
			allResults = append(allResults, aggsResp.Results...)
		}

		// Handle pagination next_url if results exceed limit
		if aggsResp.NextURL != "" {
			currentURL = aggsResp.NextURL
			// If NextURL is a relative URL, prepend BaseURL
			if !strings.HasPrefix(currentURL, "http://") && !strings.HasPrefix(currentURL, "https://") {
				currentURL = p.BaseURL + currentURL
			}
		} else {
			currentURL = ""
		}
	}

	if len(allResults) == 0 {
		return nil, fmt.Errorf("no polygon bars returned for %s between %s and %s", sym, fromStr, toStr)
	}

	bars := make([]models.Bar, 0, len(allResults))
	intervalName := req.Timeframe
	if intervalName == "" {
		intervalName = "1d"
	}

	endBound := req.EndDate
	if !endBound.IsZero() && endBound.Hour() == 0 && endBound.Minute() == 0 && endBound.Second() == 0 {
		endBound = time.Date(endBound.Year(), endBound.Month(), endBound.Day(), 23, 59, 59, 999999999, time.UTC)
	}

	for _, res := range allResults {
		t := time.UnixMilli(res.T).UTC()
		if !req.StartDate.IsZero() && t.Before(req.StartDate) {
			continue
		}
		if !endBound.IsZero() && t.After(endBound) {
			continue
		}

		dateStr := t.Format("2006-01-02")
		if timespan == "minute" || timespan == "hour" || timespan == "second" {
			dateStr = t.Format("2006-01-02 15:04:05")
		}
		bars = append(bars, models.Bar{
			Idx:        len(bars) + 1,
			Symbol:     sym,
			Date:       dateStr,
			Timeframe:  intervalName,
			AssetClass: "equity",
			Open:       res.O,
			High:       res.H,
			Low:        res.L,
			Close:      res.C,
			AdjClose:   res.C, // Polygon default adjusted=true provides split-adjusted pricing
			Volume:     int64(res.V),
			ParsedDt:   t,
		})
	}

	return bars, nil
}

// parsePolygonTimeframe converts common interval strings (e.g. "1d", "1h", "5m") to Polygon multiplier and timespan.
func parsePolygonTimeframe(tf string) (int, string) {
	raw := strings.TrimSpace(tf)
	tfLower := strings.ToLower(raw)
	if tfLower == "" || tfLower == "1d" || tfLower == "d" || tfLower == "day" || tfLower == "daily" {
		return 1, "day"
	}
	if tfLower == "1w" || tfLower == "w" || tfLower == "week" || tfLower == "weekly" {
		return 1, "week"
	}
	if tfLower == "1mo" || tfLower == "month" || tfLower == "monthly" || raw == "1M" {
		return 1, "month"
	}
	if tfLower == "1h" || tfLower == "h" || tfLower == "hour" {
		return 1, "hour"
	}

	// Minute intervals (e.g. 1m, 5m, 15m, 30m, 1min)
	if strings.HasSuffix(tfLower, "m") || strings.HasSuffix(tfLower, "min") {
		numStr := strings.TrimRight(tfLower, "min")
		if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
			return n, "minute"
		}
		return 1, "minute"
	}

	// Hour intervals (e.g. 2h, 4h)
	if strings.HasSuffix(tfLower, "h") {
		numStr := strings.TrimSuffix(tfLower, "h")
		if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
			return n, "hour"
		}
		return 1, "hour"
	}

	return 1, "day"
}

// ResolvePolygonAPIKey returns POLYGON_API_KEY (or POLYGON_KEY) from the
// environment or the project .env file (see pkg/appenv).
func ResolvePolygonAPIKey() string {
	if key := appenv.Get("POLYGON_API_KEY"); key != "" {
		return key
	}
	return appenv.Get("POLYGON_KEY")
}

// parseSimpleEnvFile extracts key-value pairs from a .env file without external dependencies.
func parseSimpleEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			// Strip surrounding single or double quotes
			v = strings.Trim(v, `"'`)
			result[k] = v
		}
	}
	return result, scanner.Err()
}

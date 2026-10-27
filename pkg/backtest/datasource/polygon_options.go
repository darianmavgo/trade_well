package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// PolygonOptions reads option reference data and EOD aggregates from Polygon.
//
// The free tier allows 5 calls/minute and ~2 years of history, so every
// request (including pagination) goes through one shared limiter. Concurrency
// cannot beat that cap, so callers should fetch serially and instead avoid
// calls (skip contracts already stored).
type PolygonOptions struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client

	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	Calls    int
}

// NewPolygonOptions builds a client. callsPerMinute <= 0 disables throttling (paid tiers).
func NewPolygonOptions(apiKey string, callsPerMinute int) *PolygonOptions {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = ResolvePolygonAPIKey()
	}
	p := &PolygonOptions{APIKey: key, BaseURL: "https://api.polygon.io", HTTP: &http.Client{Timeout: 30 * time.Second}}
	if callsPerMinute > 0 {
		p.interval = time.Minute / time.Duration(callsPerMinute)
	}
	return p
}

func (p *PolygonOptions) wait(ctx context.Context) error {
	if p.interval == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := p.interval - time.Since(p.last); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.last = time.Now()
	return nil
}

func (p *PolygonOptions) get(ctx context.Context, u string, out any) error {
	if p.APIKey == "" {
		return fmt.Errorf("polygon API key is missing. Set POLYGON_API_KEY in environment or .env, or pass -polygon-key")
	}
	for attempt := 0; ; attempt++ {
		if err := p.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		req.Header.Set("User-Agent", "BacktestGoSQLite/1.0")
		resp, err := p.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		p.Calls++
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case http.StatusOK:
			return json.Unmarshal(body, out)
		case http.StatusTooManyRequests:
			if attempt >= 3 {
				return fmt.Errorf("polygon rate limit (HTTP 429) persisted after retries")
			}
			select { // limiter drifted; back off a full window
			case <-time.After(time.Minute):
			case <-ctx.Done():
				return ctx.Err()
			}
		case http.StatusForbidden:
			return fmt.Errorf("polygon HTTP 403 (outside plan entitlement, e.g. history older than 2 years): %s", string(body))
		default:
			return fmt.Errorf("polygon HTTP %d: %s", resp.StatusCode, string(body))
		}
	}
}

type polyContractsResp struct {
	Results []struct {
		Ticker            string  `json:"ticker"`
		Underlying        string  `json:"underlying_ticker"`
		Expiry            string  `json:"expiration_date"`
		Strike            float64 `json:"strike_price"`
		Type              string  `json:"contract_type"`
		SharesPerContract int     `json:"shares_per_contract"`
	} `json:"results"`
	NextURL string `json:"next_url"`
}

// ListContracts lists contracts for one underlying/expiry. Expired chains need expired=true.
func (p *PolygonOptions) ListContracts(ctx context.Context, underlying, expiry, contractType string, expired bool) ([]models.OptionContract, error) {
	u := fmt.Sprintf("%s/v3/reference/options/contracts?underlying_ticker=%s&contract_type=%s&expiration_date=%s&expired=%t&limit=1000&sort=strike_price",
		p.BaseURL, url.QueryEscape(underlying), contractType, expiry, expired)
	var out []models.OptionContract
	for u != "" {
		var r polyContractsResp
		if err := p.get(ctx, u, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Results {
			spc := c.SharesPerContract
			if spc == 0 {
				spc = 100
			}
			out = append(out, models.OptionContract{Ticker: c.Ticker, Underlying: c.Underlying, Expiry: c.Expiry,
				Strike: c.Strike, Right: c.Type, SharesPerContract: spc})
		}
		u = r.NextURL
	}
	return out, nil
}

// Bars fetches daily aggregates for one option ticker. An empty result is not an error:
// a contract that never traded in the window simply has no bars.
func (p *PolygonOptions) Bars(ctx context.Context, ticker, from, to string) ([]models.OptionBar, error) {
	u := fmt.Sprintf("%s/v2/aggs/ticker/%s/range/1/day/%s/%s?adjusted=true&sort=asc&limit=50000",
		p.BaseURL, url.PathEscape(ticker), from, to)
	var out []models.OptionBar
	for u != "" {
		var r PolygonAggsResponse
		if err := p.get(ctx, u, &r); err != nil {
			return nil, err
		}
		for _, a := range r.Results {
			out = append(out, models.OptionBar{Ticker: ticker, Date: time.UnixMilli(a.T).UTC().Format("2006-01-02"),
				Open: a.O, High: a.H, Low: a.L, Close: a.C, Volume: int64(a.V), Trades: a.N})
		}
		u = r.NextURL
	}
	return out, nil
}

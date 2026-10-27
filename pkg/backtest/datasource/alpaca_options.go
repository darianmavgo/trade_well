package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// AlpacaOptions reads option contracts (trading API, expired ones included) and
// daily option bars (market data API) from Alpaca. Alpaca's option history
// starts in early 2024.
type AlpacaOptions struct {
	KeyID, Secret string
	TradingURL    string // contracts; ALPACA_ENDPOINT without the /v2 suffix
	DataURL       string // bars
	HTTP          *http.Client
	RetryWait     time.Duration // after an HTTP 429 (default 20s), retried 5 times
	Calls         int
}

// NewAlpacaOptions builds the client; empty keys fall back to ALPACA_KEY / ALPACA_SECRET.
func NewAlpacaOptions(keyID, secret string) *AlpacaOptions {
	if strings.TrimSpace(keyID) == "" || strings.TrimSpace(secret) == "" {
		keyID, secret = ResolveAlpacaKeys()
	}
	trading := strings.TrimRight(strings.TrimSpace(appenv.Get("ALPACA_ENDPOINT")), "/")
	trading = strings.TrimSuffix(trading, "/v2")
	if trading == "" {
		trading = "https://paper-api.alpaca.markets"
	}
	return &AlpacaOptions{KeyID: strings.TrimSpace(keyID), Secret: strings.TrimSpace(secret),
		TradingURL: trading, DataURL: "https://data.alpaca.markets", HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (a *AlpacaOptions) get(ctx context.Context, u string, out any) error {
	if a.KeyID == "" || a.Secret == "" {
		return fmt.Errorf("alpaca keys are missing. Set ALPACA_KEY and ALPACA_SECRET in the environment or .env, or pass -alpaca-key/-alpaca-secret")
	}
	wait := a.RetryWait
	if wait <= 0 {
		wait = 20 * time.Second
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("APCA-API-KEY-ID", a.KeyID)
		req.Header.Set("APCA-API-SECRET-KEY", a.Secret)
		a.Calls++
		resp, err := a.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 5 {
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("alpaca %s: HTTP %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return json.Unmarshal(body, out)
	}
}

type alpacaContractsResponse struct {
	Contracts []struct {
		Symbol     string `json:"symbol"`
		Underlying string `json:"underlying_symbol"`
		Expiry     string `json:"expiration_date"`
		Type       string `json:"type"`
		Strike     string `json:"strike_price"`
		Size       string `json:"size"`
	} `json:"option_contracts"`
	NextPageToken string `json:"next_page_token"`
}

// ListContracts lists every call and put on underlying expiring on or after
// from (YYYY-MM-DD), active and expired. Adjusted contracts (after a split) carry
// their own deliverable in SharesPerContract.
func (a *AlpacaOptions) ListContracts(ctx context.Context, underlying, from string) ([]models.OptionContract, error) {
	var out []models.OptionContract
	for _, status := range []string{"active", "inactive"} {
		token := ""
		for {
			q := url.Values{"underlying_symbols": {underlying}, "status": {status}, "limit": {"1000"}}
			if from != "" {
				q.Set("expiration_date_gte", from)
			}
			if token != "" {
				q.Set("page_token", token)
			}
			var r alpacaContractsResponse
			if err := a.get(ctx, a.TradingURL+"/v2/options/contracts?"+q.Encode(), &r); err != nil {
				return nil, err
			}
			for _, c := range r.Contracts {
				strike, err := strconv.ParseFloat(c.Strike, 64)
				if err != nil {
					continue
				}
				size, _ := strconv.Atoi(c.Size)
				if size <= 0 {
					size = 100
				}
				out = append(out, models.OptionContract{Ticker: c.Symbol, Underlying: c.Underlying, Expiry: c.Expiry,
					Strike: strike, Right: c.Type, SharesPerContract: size})
			}
			if r.NextPageToken == "" {
				break
			}
			token = r.NextPageToken
		}
	}
	return out, nil
}

type alpacaOptionBarsResponse struct {
	Bars map[string][]struct {
		T string  `json:"t"`
		O float64 `json:"o"`
		H float64 `json:"h"`
		L float64 `json:"l"`
		C float64 `json:"c"`
		V int64   `json:"v"`
		N int64   `json:"n"`
	} `json:"bars"`
	NextPageToken string `json:"next_page_token"`
}

// Bars returns daily bars for several contracts at once (Alpaca takes a list of
// symbols per request). A contract that never traded in the window has no entry.
func (a *AlpacaOptions) Bars(ctx context.Context, tickers []string, from, to string) (map[string][]models.OptionBar, error) {
	out := map[string][]models.OptionBar{}
	token := ""
	for {
		q := url.Values{"symbols": {strings.Join(tickers, ",")}, "timeframe": {"1Day"}, "start": {from}, "limit": {"10000"}}
		if to != "" {
			q.Set("end", to)
		}
		if token != "" {
			q.Set("page_token", token)
		}
		var r alpacaOptionBarsResponse
		if err := a.get(ctx, a.DataURL+"/v1beta1/options/bars?"+q.Encode(), &r); err != nil {
			return nil, err
		}
		for sym, bars := range r.Bars {
			for _, b := range bars {
				if len(b.T) < 10 {
					continue
				}
				out[sym] = append(out[sym], models.OptionBar{Ticker: sym, Date: b.T[:10],
					Open: b.O, High: b.H, Low: b.L, Close: b.C, Volume: b.V, Trades: b.N})
			}
		}
		if r.NextPageToken == "" {
			return out, nil
		}
		token = r.NextPageToken
	}
}

package ibkr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ListLinkedAccounts is schwab.Client.ListLinkedAccounts: the accounts this login can
// trade. HashValue equals AccountNumber (IBKR has no account hash).
func (c *Client) ListLinkedAccounts(ctx context.Context) ([]LinkedAccount, error) {
	raw, err := c.TradingPortfolioGetAllAccounts(ctx)
	if err != nil {
		return nil, err
	}
	var accts []struct {
		AccountID string `json:"accountId"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(raw, &accts); err != nil {
		return nil, fmt.Errorf("decoding /portfolio/accounts: %w", err)
	}
	out := make([]LinkedAccount, 0, len(accts))
	for _, a := range accts {
		id := a.AccountID
		if id == "" {
			id = a.ID
		}
		if id != "" {
			out = append(out, LinkedAccount{AccountNumber: id, HashValue: id})
		}
	}
	// IBKR requires /iserver/accounts to be called once before order endpoints work.
	_, _ = c.TradingAccountsGetBrokerageAccounts(ctx)
	return out, nil
}

// flexAmount reads IBKR's {"amount": 1.0, "currency": "USD"} summary cells as well as bare numbers.
func flexAmount(m map[string]json.RawMessage, keys ...string) float64 {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var cell struct {
			Amount FlexNumber `json:"amount"`
		}
		if json.Unmarshal(raw, &cell) == nil && cell.Amount != 0 {
			return float64(cell.Amount)
		}
		var n FlexNumber
		if json.Unmarshal(raw, &n) == nil && n != 0 {
			return float64(n)
		}
	}
	return 0
}

// GetAccount is schwab.Client.GetAccount. It returns the account as schwaber's
// {"securitiesAccount": {type, accountNumber, currentBalances, positions[]}} JSON, built
// from the account summary and, when fields contains "positions", the positions list, so
// trader.ParseSecuritiesAccount reads it unchanged.
//
// Mapping: cashBalance = totalcashvalue, cashAvailableForTrading = availablefunds,
// liquidationValue = netliquidation, buyingPower = buyingpower; a position's longQuantity
// / shortQuantity are the positive / negative sides of IBKR's signed "position", and
// averagePrice, marketValue come straight across. currentDayProfitLoss is not in IBKR's
// positions payload and stays 0.
func (c *Client) GetAccount(ctx context.Context, accountID, fields string) (*Account, error) {
	rawSummary, err := c.TradingPortfolioGetPortfolioSummary(ctx, accountID, nil)
	if err != nil {
		return nil, err
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		return nil, fmt.Errorf("decoding portfolio summary: %w", err)
	}
	sec := map[string]any{
		"type":          "IBKR",
		"accountNumber": accountID,
		"currentBalances": map[string]float64{
			"cashBalance":             flexAmount(summary, "totalcashvalue", "cashbalance"),
			"cashAvailableForTrading": flexAmount(summary, "availablefunds", "totalcashvalue"),
			"liquidationValue":        flexAmount(summary, "netliquidation"),
			"buyingPower":             flexAmount(summary, "buyingpower"),
		},
	}
	if strings.Contains(fields, "positions") {
		positions, err := c.ListPositions(ctx, accountID)
		if err != nil {
			return nil, err
		}
		sec["positions"] = positions
	}
	data, err := json.Marshal(sec) // the inner object, as schwab.Account.SecuritiesAccount holds it
	if err != nil {
		return nil, err
	}
	return &Account{SecuritiesAccount: data}, nil
}

// SchwabPosition is a position in schwaber's JSON shape.
type SchwabPosition struct {
	ShortQuantity        float64        `json:"shortQuantity"`
	AveragePrice         float64        `json:"averagePrice"`
	CurrentDayProfitLoss float64        `json:"currentDayProfitLoss"`
	LongQuantity         float64        `json:"longQuantity"`
	MarketValue          float64        `json:"marketValue"`
	Instrument           map[string]any `json:"instrument"`
}

// ListPositions reads an account's positions (uncached) in schwaber's shape.
func (c *Client) ListPositions(ctx context.Context, accountID string) ([]SchwabPosition, error) {
	raw, err := c.TradingPortfolioGetUncachedPositions(ctx, accountID, nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Conid        FlexString `json:"conid"`
		Ticker       string     `json:"ticker"`
		Description  string     `json:"description"` // the symbol, in IBKR's positions payload
		ContractDesc string     `json:"contractDesc"`
		AssetClass   string     `json:"assetClass"`
		Position     FlexNumber `json:"position"`
		AvgPrice     FlexNumber `json:"avgPrice"`
		AvgCost      FlexNumber `json:"avgCost"`
		MktValue     FlexNumber `json:"marketValue"` // IBKR calls it marketValue here (mktValue elsewhere)
		MktValueAlt  FlexNumber `json:"mktValue"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decoding positions: %w", err)
	}
	out := make([]SchwabPosition, 0, len(rows))
	for _, r := range rows {
		if r.Position == 0 {
			continue
		}
		sym := r.Ticker
		for _, cand := range []string{r.Description, r.ContractDesc} {
			if f := strings.Fields(cand); sym == "" && len(f) > 0 {
				sym = f[0]
			}
		}
		if sym == "" {
			sym = "conid:" + string(r.Conid)
		}
		avg := float64(r.AvgPrice)
		if avg == 0 {
			avg = float64(r.AvgCost)
		}
		p := SchwabPosition{AveragePrice: avg, MarketValue: firstNonZero(float64(r.MktValue), float64(r.MktValueAlt)),
			Instrument: map[string]any{"symbol": sym, "assetType": assetType(r.AssetClass)}}
		if r.Position > 0 {
			p.LongQuantity = float64(r.Position)
		} else {
			p.ShortQuantity = float64(-r.Position)
		}
		out = append(out, p)
	}
	return out, nil
}

func assetType(assetClass string) string {
	switch strings.ToUpper(assetClass) {
	case "STK", "":
		return "EQUITY"
	case "OPT":
		return "OPTION"
	case "FUT":
		return "FUTURE"
	case "CASH":
		return "FOREX"
	default:
		return strings.ToUpper(assetClass)
	}
}

// ResolveConid finds the IB contract id of a US stock/ETF symbol (cached per Client).
func (c *Client) ResolveConid(ctx context.Context, symbol string) (int64, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return 0, fmt.Errorf("empty symbol")
	}
	c.conidMu.Lock()
	id, ok := c.conids[symbol]
	c.conidMu.Unlock()
	if ok {
		return id, nil
	}
	q := url.Values{"symbol": {symbol}, "secType": {"STK"}}
	raw, err := c.TradingContractsGetContractSymbols(ctx, q)
	if err != nil {
		return 0, err
	}
	var hits []struct {
		Conid    FlexString `json:"conid"`
		Symbol   string     `json:"symbol"`
		Sections []struct {
			SecType string `json:"secType"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &hits); err != nil {
		return 0, fmt.Errorf("decoding secdef search for %s: %w", symbol, err)
	}
	for _, h := range hits {
		if !strings.EqualFold(h.Symbol, symbol) {
			continue
		}
		stk := len(h.Sections) == 0
		for _, s := range h.Sections {
			stk = stk || s.SecType == "STK"
		}
		var n int64
		if _, err := fmt.Sscan(string(h.Conid), &n); err == nil && n > 0 && stk {
			c.conidMu.Lock()
			c.conids[symbol] = n
			c.conidMu.Unlock()
			return n, nil
		}
	}
	return 0, fmt.Errorf("no stock contract found for %s", symbol)
}

// GetQuote is schwab.Client.GetQuote: a market-data snapshot in schwaber's quote shape
// (lastPrice, bidPrice, askPrice, openPrice, highPrice, lowPrice, closePrice, symbol).
// IBKR answers the first snapshot request for a contract empty (it starts the
// subscription), so this asks up to three times.
func (c *Client) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	conid, err := c.ResolveConid(ctx, symbol)
	if err != nil {
		return nil, err
	}
	q := url.Values{"conids": {fmt.Sprint(conid)}, "fields": {"31,84,86,70,71,7295,7741,55"}}
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(700 * time.Millisecond) // the first request only starts the subscription
		}
		raw, err := c.TradingMarketDataGetMdSnapshot(ctx, q)
		if err != nil {
			return nil, err
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("decoding snapshot: %w", err)
		}
		if len(rows) > 0 {
			field := func(tag string) float64 {
				var n FlexNumber
				_ = json.Unmarshal(rows[0][tag], &n)
				return float64(n)
			}
			if last := field("31"); last > 0 || field("84") > 0 || field("86") > 0 {
				body, _ := json.Marshal(map[string]any{
					"symbol": strings.ToUpper(symbol), "lastPrice": last, "bidPrice": field("84"), "askPrice": field("86"),
					"highPrice": field("70"), "lowPrice": field("71"), "openPrice": field("7295"), "closePrice": field("7741"),
				})
				rt := true
				return &Quote{AssetMainType: "EQUITY", Realtime: &rt, Quote: body}, nil
			}
		}
	}
	return nil, fmt.Errorf("quote not available for symbol: %s (no market data subscription, or the market is closed)", symbol)
}

var _ = http.MethodGet

func firstNonZero(vs ...float64) float64 {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}

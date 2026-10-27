package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

type TraderClient struct {
	Client *schwab.Client
	Config *config.Config
	Safety *SafetyGuard
}

// NewTraderClient creates an authenticated Schwab client using the native Schwab API client.
func NewTraderClient(ctx context.Context, cfg *config.Config) (*TraderClient, error) {
	if cfg.AppKey == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("missing SCHWAB_APP_KEY or SCHWAB_APP_SECRET in .env")
	}

	httpClient := NewLoggingHTTPClient(cfg.Debug, cfg.LogFile)

	opts := []schwab.Option{
		schwab.WithTokenPath(cfg.TokenPath),
		schwab.WithCallbackURL(cfg.CallbackURL),
		schwab.WithHTTPClient(httpClient),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, schwab.WithBaseURL(cfg.BaseURL))
	}

	client, err := schwab.NewClient(ctx, cfg.AppKey, cfg.AppSecret, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Schwab client: %w", err)
	}
	// Durable order intents live in the shared Turso database so PlaceOrder
	// records "about to place" before HTTP (kill-safe idempotency).
	if err := client.EnsureIntentStore(); err != nil {
		return nil, fmt.Errorf("order intent store: %w", err)
	}

	return &TraderClient{
		Client: client,
		Config: cfg,
		Safety: NewSafetyGuard(),
	}, nil
}

// GetAccounts resolves a specific, explicitly targeted set of Schwab accounts
// by account number. It performs no auto-detection: every
// requested account number must be found among the linked accounts, or the
// call fails and names exactly which account numbers were not found.
func (tc *TraderClient) GetAccounts(ctx context.Context, targets []string) ([]*ResolvedAccount, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no account numbers requested")
	}

	linked, err := tc.Client.ListLinkedAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list linked accounts: %w", err)
	}
	wanted := make([]string, len(targets))
	for i, t := range targets {
		wanted[i] = strings.TrimSpace(t)
	}
	matched, err := FilterLinkedAccounts(linked, wanted)
	if err != nil {
		return nil, err
	}

	var resolved []*ResolvedAccount
	for _, la := range matched {
		fullAcc, err := tc.Client.GetAccount(ctx, la.HashValue, "positions")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch account %s: %w", la.AccountNumber, err)
		}
		sec, err := ParseSecuritiesAccount(fullAcc.SecuritiesAccount)
		if err != nil {
			return nil, fmt.Errorf("failed to parse account %s: %w", la.AccountNumber, err)
		}

		resolved = append(resolved, &ResolvedAccount{
			AccountNumber: la.AccountNumber,
			HashValue:     la.HashValue,
			Details:       *sec,
		})
	}

	return resolved, nil
}

// ExtractQuoteLastPrice parses the current market price from a schwab.Quote response.
func ExtractQuoteLastPrice(quote *schwab.Quote) float64 {
	if quote == nil || len(quote.Quote) == 0 {
		return 0.0
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(quote.Quote, &rawMap); err != nil {
		return 0.0
	}

	for _, key := range []string{"lastPrice", "regularMarketLastPrice", "askPrice", "bidPrice", "closePrice"} {
		if val, ok := rawMap[key]; ok {
			if f, ok := val.(float64); ok && f > 0 {
				return f
			}
		}
	}
	return 0.0
}

// ExtractQuoteAskPrice prefers ask, then last — use for BUY limit repricing.
func ExtractQuoteAskPrice(quote *schwab.Quote) float64 {
	if quote == nil || len(quote.Quote) == 0 {
		return 0
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(quote.Quote, &rawMap); err != nil {
		return 0
	}
	for _, key := range []string{"askPrice", "lastPrice", "regularMarketLastPrice", "mark", "closePrice"} {
		if val, ok := rawMap[key]; ok {
			if f, ok := val.(float64); ok && f > 0 {
				return f
			}
		}
	}
	return 0
}

// FilterLinkedAccounts returns the linked accounts matching targets, in
// target order. Any target that is not linked on this Schwab login fails the
// whole call, naming the missing and the linked account numbers.
func FilterLinkedAccounts(linked []schwab.LinkedAccount, targets []string) ([]schwab.LinkedAccount, error) {
	byNum := make(map[string]schwab.LinkedAccount, len(linked))
	var linkedNums []string
	for _, a := range linked {
		byNum[a.AccountNumber] = a
		linkedNums = append(linkedNums, a.AccountNumber)
	}
	var out []schwab.LinkedAccount
	var missing []string
	for _, t := range targets {
		a, ok := byNum[t]
		if !ok {
			missing = append(missing, t)
			continue
		}
		out = append(out, a)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("configured account(s) not linked on this Schwab login: %s (linked: %s)",
			strings.Join(missing, ", "), strings.Join(linkedNums, ", "))
	}
	return out, nil
}

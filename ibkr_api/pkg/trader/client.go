// Package trader is the IBKR twin of schwaber/pkg/trader. Everything broker-neutral IS
// schwaber's (types and functions are re-exported, not copied): the order builders, the
// safety guard, ResolvedAccount, ParseSecuritiesAccount, GetBuyingPower, the quote
// helpers. Because ibkr.Order is schwab.Order they take IBKR values unchanged. What is
// IBKR's own is TraderClient: building the client, its intent store and account
// resolution.
package trader

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/darianmavgo/ibkr_api/pkg/config"
	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	"github.com/darianmavgo/schwaber/pkg/orderintent"
	schwabtrader "github.com/darianmavgo/schwaber/pkg/trader"
)

// Re-exports of schwaber's trader package.
type (
	ResolvedAccount   = schwabtrader.ResolvedAccount
	SecuritiesAccount = schwabtrader.SecuritiesAccount
	Position          = schwabtrader.Position
	Balances          = schwabtrader.Balances
	OrderLeg          = schwabtrader.OrderLeg
	SafetyGuard       = schwabtrader.SafetyGuard
	ValidationResult  = schwabtrader.ValidationResult
)

const (
	OrderTypeMarket    = schwabtrader.OrderTypeMarket
	OrderTypeLimit     = schwabtrader.OrderTypeLimit
	OrderTypeStop      = schwabtrader.OrderTypeStop
	OrderTypeStopLimit = schwabtrader.OrderTypeStopLimit
	StrategySingle     = schwabtrader.StrategySingle
	StrategyTrigger    = schwabtrader.StrategyTrigger
	StrategyOCO        = schwabtrader.StrategyOCO
	InstructionBuy     = schwabtrader.InstructionBuy
	InstructionSell    = schwabtrader.InstructionSell
	DurationDay        = schwabtrader.DurationDay
	DurationGTC        = schwabtrader.DurationGTC
	SessionNormal      = schwabtrader.SessionNormal
	CrisisStopFraction = schwabtrader.CrisisStopFraction
)

var (
	BuildOrderSpec         = schwabtrader.BuildOrderSpec
	BuildBracketOrderSpec  = schwabtrader.BuildBracketOrderSpec
	ParseOrderLegs         = schwabtrader.ParseOrderLegs
	ParseSecuritiesAccount = schwabtrader.ParseSecuritiesAccount
	GetBuyingPower         = schwabtrader.GetBuyingPower
	ExtractQuoteLastPrice  = schwabtrader.ExtractQuoteLastPrice
	ExtractQuoteAskPrice   = schwabtrader.ExtractQuoteAskPrice
	NewSafetyGuard         = schwabtrader.NewSafetyGuard
	RoundPrice             = schwabtrader.RoundPrice
)

type TraderClient struct {
	Client *ibkr.Client
	Config *config.Config
	Safety *SafetyGuard
}

// NewTraderClient builds the client from cfg. The HTTP client is schwaber's logging one
// (same log format, same redaction); for a gateway on this machine it trusts the
// gateway's self-signed certificate. Durable order intents live in cfg.DBPath (ibkr.db),
// so PlaceOrder records "about to place" before any request, as schwaber does.
func NewTraderClient(ctx context.Context, cfg *config.Config) (*TraderClient, error) {
	if err := checkModeMatchesAccount(cfg); err != nil {
		return nil, err
	}
	h := schwabtrader.NewLoggingHTTPClient(cfg.Debug, cfg.LogFile)
	base := cfg.BaseURL
	if base == "" && cfg.ClientID == "" && cfg.OAuth1ConsumerKey == "" {
		base = cfg.GatewayURL
	}
	if lr, ok := h.Transport.(*schwabtrader.LoggingRoundTripper); ok && ibkr.IsLoopbackURL(base) {
		if tr, ok := lr.Transport.(*http.Transport); ok {
			if tr.TLSClientConfig == nil {
				tr.TLSClientConfig = insecureLoopbackTLS()
			} else {
				tr.TLSClientConfig.InsecureSkipVerify = true // #nosec G402: loopback gateway, self-signed by IBKR
			}
		}
	}
	store, err := orderintent.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("order intent store: %w", err)
	}
	client, err := ibkr.NewFromConfig(ctx, cfg, ibkr.WithHTTPClient(h), ibkr.WithIntentStore(store))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IBKR client: %w", err)
	}
	return &TraderClient{Client: client, Config: cfg, Safety: NewSafetyGuard()}, nil
}

// GetAccounts resolves an explicitly targeted set of accounts by account id, exactly like
// schwaber's TraderClient.GetAccounts: no auto-detection, every requested account must be
// found or the call fails naming the ones that were not. HashValue is the account id.
func (tc *TraderClient) GetAccounts(ctx context.Context, targets []string) ([]*ResolvedAccount, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no account ids requested")
	}
	linked, err := tc.Client.ListLinkedAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list linked accounts: %w", err)
	}
	matched, err := FilterLinkedAccounts(linked, trimAll(targets))
	if err != nil {
		return nil, err
	}
	var resolved []*ResolvedAccount
	for _, la := range matched {
		full, err := tc.Client.GetAccount(ctx, la.HashValue, "positions")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch account %s: %w", la.AccountNumber, err)
		}
		sec, err := ParseSecuritiesAccount(full.SecuritiesAccount)
		if err != nil {
			return nil, fmt.Errorf("failed to parse account %s: %w", la.AccountNumber, err)
		}
		resolved = append(resolved, &ResolvedAccount{AccountNumber: la.AccountNumber, HashValue: la.HashValue, Details: *sec})
	}
	return resolved, nil
}

func trimAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

// FilterLinkedAccounts returns the linked accounts matching targets, in target order; any
// target not linked on this login fails the call, naming missing and linked ids.
func FilterLinkedAccounts(linked []ibkr.LinkedAccount, targets []string) ([]ibkr.LinkedAccount, error) {
	byNum := map[string]ibkr.LinkedAccount{}
	var nums []string
	for _, a := range linked {
		byNum[a.AccountNumber] = a
		nums = append(nums, a.AccountNumber)
	}
	var out []ibkr.LinkedAccount
	var missing []string
	for _, t := range targets {
		if a, ok := byNum[t]; ok {
			out = append(out, a)
		} else {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("configured account(s) not linked on this IBKR login: %s (linked: %s)", strings.Join(missing, ", "), strings.Join(nums, ", "))
	}
	return out, nil
}

// checkModeMatchesAccount refuses a paper/live setting that contradicts the account id:
// IBKR paper accounts start with "DU"/"DF". Trading the wrong one is never intended.
func checkModeMatchesAccount(cfg *config.Config) error {
	if cfg.TradingMode == "" {
		return nil
	}
	for _, id := range cfg.AccountIDs {
		paper := strings.HasPrefix(id, "DU") || strings.HasPrefix(id, "DF")
		if cfg.TradingMode == "paper" && !paper {
			return fmt.Errorf("IBKR_TRADING_MODE=paper but account %s is not a paper account (paper ids start with DU/DF)", id)
		}
		if cfg.TradingMode == "live" && paper {
			return fmt.Errorf("IBKR_TRADING_MODE=live but account %s is a paper account", id)
		}
	}
	return nil
}

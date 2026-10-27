package strategy_status

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// ordersLookback is how far back live orders are read: Schwab's list window is 60 days,
// and an entry rests as a GTC limit for weeks.
const ordersLookback = 58 * 24 * time.Hour

// Live is what Schwab says right now for the accounts the status covers. Accounts whose
// fetch failed are absent from Accounts, so nothing is concluded from their silence.
type Live struct {
	FetchedAt time.Time
	// Accounts are the account numbers whose positions AND orders were both read.
	Accounts map[string]bool
	// Held is the long quantity per "account|SYMBOL".
	Held map[string]float64
	// Orders are each account's orders, top-level (bracket children nested).
	Orders map[string][]schwab.Order
	Errors []string
}

// FetchLive reads positions and orders for targets from Schwab. Partial failure is
// reported in Errors, never hidden, and never turned into "no position".
func FetchLive(ctx context.Context, tc *trader.TraderClient, targets []string, now time.Time) *Live {
	l := &Live{FetchedAt: now, Accounts: map[string]bool{}, Held: map[string]float64{}, Orders: map[string][]schwab.Order{}}
	if tc == nil {
		l.Errors = append(l.Errors, "no Schwab client")
		return l
	}
	accs, err := tc.GetAccounts(ctx, targets)
	if err != nil {
		l.Errors = append(l.Errors, fmt.Sprintf("could not read live accounts: %v", err))
		return l
	}
	for _, acc := range accs {
		orders, err := tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{From: now.Add(-ordersLookback), To: now})
		if err != nil {
			l.Errors = append(l.Errors, fmt.Sprintf("could not read live orders for account %s: %v", acc.AccountNumber, err))
			continue
		}
		l.Accounts[acc.AccountNumber] = true
		l.Orders[acc.AccountNumber] = orders
		for _, p := range acc.Details.Positions {
			if p.LongQuantity > 0 {
				l.Held[acc.AccountNumber+"|"+strings.ToUpper(strings.TrimSpace(p.Instrument.Symbol))] += p.LongQuantity
			}
		}
	}
	return l
}

// FetchLiveFromEnv is FetchLive for the accounts named by the environment (the account allowlist),
// with the Schwab client built from the app config. newClient builds the client; callers pass
// trader.NewTraderClient. Building it is bounded by initTimeout and the reads by callTimeout. It
// never fails: problems come back in Live.Errors, so a page can show them.
func FetchLiveFromEnv(ctx context.Context, now time.Time, newClient func(context.Context, *config.Config) (*trader.TraderClient, error), initTimeout, callTimeout time.Duration) *Live {
	fail := func(format string, args ...any) *Live {
		return &Live{FetchedAt: now, Errors: []string{fmt.Sprintf(format, args...)}}
	}
	cfg, err := appconfig.Load()
	if err != nil {
		return fail("config: %v", err)
	}
	targets, err := account_allowlist.FromEnv()
	if err != nil {
		return fail("accounts: %v", err)
	}
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	tc, err := newClient(initCtx, cfg.Schwab)
	if err != nil {
		return fail("Schwab client: %v", err)
	}
	callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
	defer cancelCall()
	return FetchLive(callCtx, tc, targets, now)
}

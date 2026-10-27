package dashboard

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// handleStageOrders is the authenticated, by-hand view of the stage_orders job:
// GET /stage_orders runs stage_orders.RunJob as a DRY RUN against your live
// account (real cash, real positions) and prints what it would stage. It never
// writes: staging for real is the scheduled /cron/stage_orders or
// `orchestrator stage_orders --live`. /cron/* refuses browser requests (only
// App Engine's cron header passes), so this is how you preview a run.
func (s *Server) handleStageOrders(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(msg string, err error) {
		log.Printf("[dashboard] stage_orders preview: %s: %v", msg, err)
		http.Error(w, "ERROR: "+msg+": "+err.Error(), http.StatusInternalServerError)
	}
	if err := s.ensureDB(); err != nil {
		fail("database unavailable", err)
		return
	}
	cfg, err := appconfig.Load()
	if err != nil {
		fail("config", err)
		return
	}
	targets, err := account_allowlist.FromEnv()
	if err != nil {
		fail("accounts", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), schwabClientInitTimeout*4)
	defer cancel()
	tc, err := newTraderClient(ctx, cfg.Schwab)
	if err != nil {
		fail("failed to initialize Schwab client", err)
		return
	}
	accounts, err := tc.GetAccounts(ctx, targets)
	if err != nil {
		fail("failed to resolve accounts", err)
		return
	}
	strats, err := strategies.ResolveGreenlit(s.db, cfg.StrategyAllowlist)
	if err != nil {
		fail("strategies", err)
		return
	}
	members := make(map[string]strategy.Strategy, len(strats))
	for _, st := range strats {
		members[st.ID()] = st
	}
	res, err := stage_orders.RunJob(ctx, s.db, accounts, stage_orders.JobOptions{DryRun: true, Members: members})
	if err != nil {
		fail("stage_orders", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	fmt.Fprintf(&b, "stage_orders DRY RUN (nothing written)\naccounts=%d signals=%d would_stage=%d dismissed=%d\n\n", res.Accounts, res.Signals, res.Staged, res.Dismissed)
	for _, row := range res.Rows {
		fmt.Fprintf(&b, "%-10s %-16s %-6s %-6s %-10s %s\n", row.Account, row.Strategy, row.Symbol, row.Signal, row.Status, row.Detail)
	}
	if len(res.Rows) == 0 {
		b.WriteString("(no buy signals waiting: run /cron/scan_market first, or there was no signal)\n")
	}
	b.WriteString("\nDetails:\n")
	for _, d := range res.Details {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	w.Write([]byte(b.String()))
}

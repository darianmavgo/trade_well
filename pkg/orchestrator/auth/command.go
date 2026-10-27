package auth

import (
	"context"
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/schwaber/oauthcli"
)

// RunLogin is the `orchestrator auth` command: the one-time interactive OAuth
// flow (implemented in schwaber's oauthcli), with each step recorded in the
// auth table.
func RunLogin(ctx context.Context, env jobenv.Env) error {
	return oauthcli.Run(ctx, env.Cfg.Schwab, oauthcli.Options{OnEvent: Recorder(env.DB)})
}

// RunTokenCommand is the `orchestrator token pull|push` command: it delegates
// to SyncToken, which calls schwaber's tokensync (GCS-backed token object) and
// logs the attempt. A bad direction prints usage and returns jobenv.ErrReported.
func RunTokenCommand(ctx context.Context, out io.Writer, env jobenv.Env, args []string) error {
	if len(args) < 1 || (args[0] != "pull" && args[0] != "push") {
		fmt.Fprintf(out, "%sUsage:%s orchestrator token pull|push\n", jobenv.ColYellow, jobenv.ColReset)
		fmt.Fprintln(out, "  pull  Fetch the shared schwab-token from GCS into local token.json (only if newer)")
		fmt.Fprintln(out, "  push  Publish local token.json to the shared schwab-token object in GCS")
		return jobenv.ErrReported
	}
	return SyncToken(ctx, env.DB, env.Cfg.Schwab.TokenPath, args[0])
}

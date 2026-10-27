// Package oauthcli is the interactive Schwab OAuth authorization-code flow
// for terminals: print the authorize URL, read the pasted redirect URL (or
// bare code), exchange it for tokens, and verify by listing linked accounts.
// It is shared by `schwaber auth` and trade_orchestrator's `auth`.
package oauthcli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Options tunes Run. The zero value prompts on os.Stdin and writes to os.Stdout.
type Options struct {
	// Code, if non-empty, is a pre-supplied redirect URL or bare code; the
	// prompt is skipped.
	Code string
	In   io.Reader
	Out  io.Writer
	// OnEvent, if set, receives audit events: "auth_failed", "exchange_failed",
	// "issued". detail never contains token values.
	OnEvent func(event, detail string)
}

func (o Options) event(ev, detail string) {
	if o.OnEvent != nil {
		o.OnEvent(ev, detail)
	}
}

// Run walks the operator through the OAuth flow described in the package doc.
func Run(ctx context.Context, cfg *config.Config, opts Options) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	in := opts.In
	if in == nil {
		in = os.Stdin
	}

	if cfg.AppKey == "" || cfg.AppSecret == "" {
		opts.event("auth_failed", "missing SCHWAB_APP_KEY/SCHWAB_APP_SECRET")
		fmt.Fprintln(out, "Error: Missing SCHWAB_APP_KEY or SCHWAB_APP_SECRET in environment / .env file.")
		fmt.Fprintln(out, "Register an application at https://developer.schwab.com and add credentials to .env.")
		return fmt.Errorf("SCHWAB_APP_KEY and SCHWAB_APP_SECRET must be set in .env")
	}

	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		opts.event("auth_failed", "client init: "+err.Error())
		return fmt.Errorf("failed to initialize client: %w", err)
	}

	codeInput := strings.TrimSpace(opts.Code)
	if codeInput == "" {
		fmt.Fprintln(out, "1. Open the following Schwab authorization URL in your web browser:")
		fmt.Fprintf(out, "\n   %s\n\n", tc.Client.AuthorizeURL())
		fmt.Fprintln(out, "2. Log into Schwab and select the accounts you want the API to access.")
		fmt.Fprintf(out, "3. After authorization, Schwab redirects to: %s\n", cfg.CallbackURL)
		fmt.Fprintln(out, "   (The browser page may display 'This site can’t be reached' — this is expected)")
		fmt.Fprintln(out, "4. Copy the ENTIRE URL from your browser's address bar (or the code) and paste it below.")
		fmt.Fprint(out, "\nPaste callback URL or code: ")

		input, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && input == "" {
			return fmt.Errorf("failed to read input: %w", err)
		}
		codeInput = strings.TrimSpace(input)
		if codeInput == "" {
			fmt.Fprintln(out, "No input provided. Authentication cancelled.")
			return fmt.Errorf("no input provided")
		}
	}

	code, err := schwab.ExtractCodeFromURL(codeInput)
	if err != nil {
		fmt.Fprintf(out, "\nError extracting authorization code: %v\n", err)
		return err
	}

	fmt.Fprintln(out, "\nExchanging authorization code for OAuth tokens...")
	if _, err := tc.Client.ExchangeAuthCode(ctx, code); err != nil {
		opts.event("exchange_failed", err.Error())
		fmt.Fprintf(out, "Token exchange failed: %v\n", err)
		return err
	}

	fmt.Fprintln(out, "\n✓ Authentication successful!")
	fmt.Fprintf(out, "Tokens persisted securely to: %s\n", cfg.TokenPath)
	opts.event("issued", "token exchange succeeded, path="+cfg.TokenPath)

	linked, err := tc.Client.ListLinkedAccounts(ctx)
	if err != nil {
		fmt.Fprintf(out, "Warning: Tokens saved, but account verification failed: %v\n", err)
	} else {
		fmt.Fprintf(out, "✓ Discovered %d linked Schwab account(s)\n", len(linked))
	}
	return nil
}

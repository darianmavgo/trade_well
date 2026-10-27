package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/accounts"
	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/killswitch"
	"github.com/darianmavgo/trade_well/pkg/schwaber/listall"
	"github.com/darianmavgo/trade_well/pkg/schwaber/oauthcli"
	"github.com/darianmavgo/trade_well/pkg/schwaber/quote"
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(0)
	}

	ctx := context.Background()
	if _, err := config.Bootstrap(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg := config.Load()

	subcommand := os.Args[1]
	subArgs := os.Args[2:]

	// Check global debug flag
	for _, arg := range subArgs {
		if arg == "--debug" || arg == "-v" {
			cfg.Debug = true
			break
		}
	}

	switch subcommand {
	case "auth":
		if err := runAuth(ctx, cfg, subArgs); err != nil {
			os.Exit(1)
		}
	case "listall":
		if err := listall.Run(ctx, os.Stdout, cfg); err != nil {
			os.Exit(1)
		}
	case "accounts":
		runAccounts(ctx, cfg, subArgs)
	case "quote":
		runQuote(ctx, cfg, subArgs)
	case "killswitch":
		runKillswitch(ctx, cfg, subArgs)
	case "-h", "--help", "help":
		printHelp()
	default:
		fmt.Printf("%sUnknown command: %s%s\n", clicolor.Red, subcommand, clicolor.Reset)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	clicolor.Banner(os.Stdout)
	fmt.Println("Usage: schwaber <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Printf("  %slistall%s     Read everything from all Schwab accounts and save into the Turso database\n", clicolor.Green, clicolor.Reset)
	fmt.Printf("  %sauth%s        Complete one-time OAuth authentication with Charles Schwab\n", clicolor.Green, clicolor.Reset)
	fmt.Printf("  %saccounts%s    Inspect Schwab accounts, balances, and positions\n", clicolor.Green, clicolor.Reset)
	fmt.Printf("  %squote%s       Fetch real-time market quote for a symbol\n", clicolor.Green, clicolor.Reset)
	fmt.Printf("  %skillswitch%s  Market-sell open positions (all by default; optional tickers)\n", clicolor.Red, clicolor.Reset)
	fmt.Println("\nFlags for all commands:")
	fmt.Println("  --debug, -v    Print full HTTP request & response payloads from real Schwab endpoints")
	fmt.Println("  --dry-run      Preview and validate only. Commands are LIVE by default; --live is accepted, --live=false also previews")
	fmt.Println("\nExamples:")
	fmt.Println("  schwaber auth")
	fmt.Println("  schwaber accounts --positions")
	fmt.Println("  schwaber quote SOFI")
	fmt.Println("  schwaber killswitch --dry-run")
	fmt.Println("  schwaber killswitch TECL MARA -y")
	fmt.Println("  schwaber killswitch -y   # ALL positions, all accounts")
}

// -----------------------------------------------------------------------------
// Everything below is a thin wrapper: parse flags, call the pkg/<command>
// function, exit non-zero on error. The command packages print their own error
// messages, so an error here only sets the exit code. No business logic.
// -----------------------------------------------------------------------------

func exitOnError(err error) {
	if err != nil {
		os.Exit(1)
	}
}

func runAuth(ctx context.Context, cfg *config.Config, args []string) error {
	clicolor.Banner(os.Stdout)
	fmt.Printf("%s[OAuth Setup]%s Connecting to Charles Schwab Developer API...\n\n", clicolor.Yellow, clicolor.Reset)

	var codeInput string
	// Check if code or URL was passed as command line argument (e.g. schwaber auth "https://127.0.0.1/?code=...")
	for i, arg := range args {
		if (arg == "--code" || arg == "-c") && i+1 < len(args) {
			codeInput = args[i+1]
			break
		} else if !strings.HasPrefix(arg, "-") && codeInput == "" {
			codeInput = arg
		}
	}
	return oauthcli.Run(ctx, cfg, oauthcli.Options{Code: codeInput})
}

func runAccounts(ctx context.Context, cfg *config.Config, args []string) {
	fs := flag.NewFlagSet("accounts", flag.ExitOnError)
	showPositions := fs.Bool("positions", false, "Display detailed positions")
	showPositionsShort := fs.Bool("p", false, "Display detailed positions (shorthand)")
	fs.Bool("debug", false, "Enable HTTP debug logging")
	fs.Bool("v", false, "Enable HTTP debug logging (shorthand)")
	fs.Parse(args)

	exitOnError(accounts.Run(ctx, os.Stdout, cfg, accounts.Options{Positions: *showPositions || *showPositionsShort}))
}

func runQuote(ctx context.Context, cfg *config.Config, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: schwaber quote <SYMBOL>")
		os.Exit(1)
	}
	exitOnError(quote.Run(ctx, os.Stdout, cfg, strings.ToUpper(strings.TrimSpace(args[0]))))
}

// runKillswitch market-sells open long positions across linked accounts.
//
//	schwaber killswitch --dry-run
//	schwaber killswitch -y
//	schwaber killswitch TECL MARA -y
//	schwaber killswitch -account 25474194 TECL -y
func runKillswitch(ctx context.Context, cfg *config.Config, args []string) {
	fs := flag.NewFlagSet("killswitch", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview market sells without placing")
	live := fs.Bool("live", true, "Submit live market sells to Schwab (the default; --dry-run or --live=false previews)")
	yes := fs.Bool("yes", false, "Skip confirmation prompt")
	yesShort := fs.Bool("y", false, "Skip confirmation prompt (shorthand)")
	account := fs.String("account", "", "Limit to one account number (default: all linked)")
	fs.Bool("debug", false, "Enable HTTP debug logging")
	fs.Bool("v", false, "Enable HTTP debug logging (shorthand)")

	// Go's flag.Parse stops at the first non-flag, so `killswitch TECL --dry-run`
	// would ignore --dry-run and go live. Split flags vs tickers ourselves.
	var flagArgs, posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			// bool flags take no value; -account takes one
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				continue
			}
			if name == "account" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		posArgs = append(posArgs, a)
	}
	_ = fs.Parse(flagArgs)

	exitOnError(killswitch.Run(ctx, os.Stdout, os.Stdin, cfg, killswitch.Options{
		DryRun: *dryRun, Live: *live, Yes: *yes || *yesShort,
		Account: strings.TrimSpace(*account), Tickers: posArgs,
	}))
}

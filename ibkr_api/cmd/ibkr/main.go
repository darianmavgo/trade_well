// ibkr is the command line for ibkr_api, shaped like schwaber's CLI: a thin wrapper that
// loads configuration, builds the client and calls the pkg/ functions.
//
//	ibkr gateway setup|start|stop|status|open|login|keepalive   manage the Client Portal Gateway
//	ibkr auth                 check the session (OAuth/gateway) and say how to log in
//	ibkr accounts             linked accounts, cash, value
//	ibkr account_state        live positions + orders into ibkr.db (account_state_* tables)
//	ibkr quote SYMBOL
//	ibkr buy|sell SYMBOL QTY [PRICE]   [-account A] [-live] [-dry-run] [-y]
//	ibkr bracket SYMBOL QTY ENTRY [-profit P] [-stop S] [-live] ...
//	ibkr orders [-status WORKING]      ibkr cancel ORDER_ID
//	ibkr killswitch [-live] [-y] [-account A] [TICKER...]
//	ibkr reconcile            resolve pending order intents against the broker
//
// Commands that talk to IBKR in gateway mode first make sure the gateway is running and
// logged in (starting it and opening the login page if not); -no-gateway skips that.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/account_state"
	"github.com/darianmavgo/ibkr_api/pkg/accounts"
	"github.com/darianmavgo/ibkr_api/pkg/config"
	"github.com/darianmavgo/ibkr_api/pkg/gateway"
	"github.com/darianmavgo/ibkr_api/pkg/ibkr"
	"github.com/darianmavgo/ibkr_api/pkg/killswitch"
	"github.com/darianmavgo/ibkr_api/pkg/orders"
	"github.com/darianmavgo/ibkr_api/pkg/quote"
	"github.com/darianmavgo/ibkr_api/pkg/trader"
	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Print(usage)
		return
	}
	ctx := context.Background()
	if _, err := config.Bootstrap(ctx); err != nil {
		fail(err)
	}
	cfg := config.Load()
	cmd, args := os.Args[1], os.Args[2:]

	if cmd == "gateway" {
		runGateway(ctx, cfg, args)
		return
	}
	noGateway := false
	rest := args[:0:0]
	for _, a := range args {
		if a == "-no-gateway" || a == "--no-gateway" {
			noGateway = true
		} else if a == "-v" || a == "--debug" {
			cfg.Debug = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest

	if !isGatewayMode(cfg) {
		noGateway = true
	}
	if !noGateway {
		if err := ensureGateway(ctx, cfg); err != nil {
			fail(err)
		}
	}
	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fail(err)
	}
	out, in := os.Stdout, os.Stdin

	switch cmd {
	case "auth":
		if err := tc.Client.EnsureFreshToken(ctx); err != nil {
			fail(fmt.Errorf("%w\n(%s mode)", err, tc.Client.Mode()))
		}
		fmt.Printf("%s: session ready\n", tc.Client.Mode())
	case "accounts":
		check(accounts.Run(ctx, out, tc))
	case "account_state", "listall":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		dbPath := fs.String("db", cfg.DBPath, "SQLite file to write")
		acct := fs.String("account", "", "account id (default: IBKR_ACCOUNT_ID)")
		_ = fs.Parse(args)
		ids := cfg.AccountIDs
		if *acct != "" {
			ids = []string{*acct}
		}
		db, err := sql.Open("sqlite", *dbPath)
		check(err)
		defer db.Close()
		res, err := account_state.Run(ctx, out, db, tc.Client, ids)
		check(err)
		fmt.Printf("account_state: %d account(s), %d position(s) written to %s\n", res.Accounts, res.Positions, *dbPath)
	case "quote":
		need(args, 1, "ibkr quote SYMBOL")
		check(quote.Run(ctx, out, tc, args[0]))
	case "buy", "sell":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		acct, live, dry, yes := fs.String("account", "", "account id"), fs.Bool("live", false, "send the order for real"), fs.Bool("dry-run", false, "preview only (wins over -live)"), fs.Bool("y", false, "skip the confirmation")
		pos := parseFlags(fs, args)
		need(pos, 2, "ibkr "+cmd+" SYMBOL QTY [LIMIT_PRICE] [flags]")
		instr := trader.InstructionBuy
		if cmd == "sell" {
			instr = trader.InstructionSell
		}
		check(orders.Place(ctx, out, in, tc, orders.PlaceOptions{Instruction: instr, Symbol: pos[0], Qty: num(pos[1]), Price: optNum(pos, 2),
			Account: *acct, Live: *live, DryRun: *dry, Yes: *yes}))
	case "bracket":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		acct, live, dry, yes := fs.String("account", "", "account id"), fs.Bool("live", false, ""), fs.Bool("dry-run", false, ""), fs.Bool("y", false, "")
		profit, stop := fs.Float64("profit", 0, "take-profit price (default entry +10%)"), fs.Float64("stop", 0, "stop price (default entry -5%)")
		pos := parseFlags(fs, args)
		need(pos, 3, "ibkr bracket SYMBOL QTY ENTRY [-profit P] [-stop S] [flags]")
		check(orders.Bracket(ctx, out, in, tc, orders.BracketOptions{Symbol: pos[0], Qty: num(pos[1]), Entry: num(pos[2]), Profit: *profit, Stop: *stop,
			Account: *acct, Live: *live, DryRun: *dry, Yes: *yes}))
	case "orders":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		status := fs.String("status", "", "filter: WORKING, FILLED, CANCELED, ...")
		_ = fs.Parse(args)
		check(orders.List(ctx, out, tc, *status))
	case "cancel":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		acct := fs.String("account", "", "account id")
		pos := parseFlags(fs, args)
		need(pos, 1, "ibkr cancel ORDER_ID [-account A]")
		check(orders.Cancel(ctx, out, tc, *acct, pos[0]))
	case "killswitch":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		acct, live, dry, yes := fs.String("account", "", "account id"), fs.Bool("live", false, ""), fs.Bool("dry-run", false, ""), fs.Bool("y", false, "")
		pos := parseFlags(fs, args)
		check(killswitch.Run(ctx, out, in, tc, killswitch.Options{Account: *acct, Live: *live, DryRun: *dry, Yes: *yes, Tickers: pos}))
	case "reconcile":
		res, err := ibkr.ReconcilePendingIntents(ctx, tc.Client)
		check(err)
		fmt.Printf("reconcile: checked %d, acked %d, unchanged %d\n", res.Checked, res.Acked, res.Unchanged)
		for _, d := range res.Details {
			fmt.Println("  " + d)
		}
	default:
		fail(fmt.Errorf("unknown command %q (ibkr help)", cmd))
	}
}

const usage = `ibkr: Interactive Brokers CLI (same shape as schwaber)
  gateway setup|start|stop|status|open|login|keepalive
  auth | accounts | account_state | quote SYMBOL
  buy|sell SYMBOL QTY [PRICE]   bracket SYMBOL QTY ENTRY [-profit P] [-stop S]
  orders [-status S] | cancel ORDER_ID | killswitch [TICKER...] | reconcile
Orders preview unless -live (and are confirmed unless -y). -no-gateway skips the gateway check.
`

func isGatewayMode(cfg *config.Config) bool {
	return cfg.ClientID == "" && cfg.OAuth1ConsumerKey == ""
}

func gatewayManager(cfg *config.Config) (*gateway.Manager, error) {
	port := gateway.DefaultPort
	if u, err := url.Parse(cfg.GatewayURL); err == nil && u.Port() != "" {
		port, _ = strconv.Atoi(u.Port())
	}
	dir, ok := gateway.Locate(gateway.DefaultDirs("")...)
	if !ok {
		return nil, fmt.Errorf("the Client Portal Gateway is not installed: run `ibkr gateway setup`")
	}
	return &gateway.Manager{Dir: dir, Port: port, Out: os.Stdout}, nil
}

// ensureGateway: gateway running (started if needed) and logged in (login page opened and
// waited for if not).
func ensureGateway(ctx context.Context, cfg *config.Config) error {
	m, err := gatewayManager(cfg)
	if err != nil {
		return err
	}
	if !m.Alive(ctx) {
		fmt.Println("gateway not running: starting it")
		if m, err = gateway.Setup(ctx, gateway.SetupOptions{Dir: m.Dir, Port: m.Port, Start: true}, os.Stdout); err != nil {
			return err
		}
	}
	return m.Login(ctx, 5*time.Minute, true)
}

func runGateway(ctx context.Context, cfg *config.Config, args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("usage: ibkr gateway setup|start|stop|status|open|login|keepalive"))
	}
	sub := args[0]
	if sub == "setup" || sub == "start" {
		fs := flag.NewFlagSet("gateway "+sub, flag.ExitOnError)
		dir := fs.String("dir", "", "gateway directory (default: search, else download)")
		port := fs.Int("port", portOf(cfg), "listen port")
		_ = fs.Parse(args[1:])
		m, err := gateway.Setup(ctx, gateway.SetupOptions{Dir: *dir, Port: *port, Start: true}, os.Stdout)
		check(err)
		st, _ := m.Session(ctx)
		if st.Authenticated {
			fmt.Println("session: authenticated")
		} else {
			fmt.Printf("not logged in: run `ibkr gateway login` (or open %s)\n", m.URL())
		}
		return
	}
	m, err := gatewayManager(cfg)
	check(err)
	switch sub {
	case "stop":
		ok, err := m.Stop()
		check(err)
		fmt.Println(map[bool]string{true: "stopped.", false: "no gateway started by ibkr is running."}[ok])
	case "status":
		fmt.Printf("dir %s\nurl %s\n", m.Dir, m.URL())
		if !m.Alive(ctx) {
			fmt.Println("running: no")
			return
		}
		st, err := m.Session(ctx)
		fmt.Printf("running: yes\nauthenticated: %v (connected %v, competing %v) %v\n", st.Authenticated, st.Connected, st.Competing, errStr(err))
	case "open":
		check(gateway.OpenBrowser(m.URL()))
	case "login":
		check(m.Login(ctx, 10*time.Minute, true))
	case "keepalive":
		fmt.Println("keeping the session alive (Ctrl-C to stop)")
		check(m.Keepalive(ctx, 60*time.Second))
	default:
		fail(fmt.Errorf("unknown gateway command %q", sub))
	}
}

func portOf(cfg *config.Config) int {
	if u, err := url.Parse(cfg.GatewayURL); err == nil && u.Port() != "" {
		if p, err := strconv.Atoi(u.Port()); err == nil {
			return p
		}
	}
	return gateway.DefaultPort
}

func errStr(err error) string {
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return ""
}

// parseFlags lets flags appear before or after the positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			pos = append(pos, args[0])
			args = args[1:]
		} else if len(args) == 0 {
			break
		}
	}
	return pos
}

func need(pos []string, n int, use string) {
	if len(pos) < n {
		fail(fmt.Errorf("usage: %s", use))
	}
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		fail(fmt.Errorf("%q is not a number", s))
	}
	return v
}

func optNum(pos []string, i int) float64 {
	if len(pos) > i {
		return num(pos[i])
	}
	return 0
}

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

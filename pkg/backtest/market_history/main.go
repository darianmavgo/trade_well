package market_history

import (
	"context"
	"flag"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/universe"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// parseSymbolArgs reads leftover CLI args as tickers. Commas are separators,
// not part of the symbol, so "VOO," "IEF," and "VOO,IEF" all parse.
func parseSymbolArgs(args []string) []string {
	var out []string
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			part = strings.ToUpper(strings.TrimSpace(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func getSymbolsFromTable(settingsDbPath, tableName string, limit int) ([]string, error) {
	db, err := sqlx.Open("sqlite", settingsDbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var symbols []string
	query := fmt.Sprintf("SELECT DISTINCT symbol FROM %s WHERE symbol != ''", tableName)
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	err = db.Select(&symbols, query)
	return symbols, err
}

// expandSymbolLists replaces every token that names a symbol_lists row in the
// settings database (case-insensitive, for example etf-pre-2021-unleveraged)
// with that list's tickers, and keeps the other tokens as tickers. Each symbol
// appears once, in first-seen order. A missing settings database leaves the
// tokens as they are.
func expandSymbolLists(settingsDB string, tokens []string) ([]string, error) {
	var db *sqlx.DB
	if settingsDB != "" {
		if _, err := os.Stat(settingsDB); err == nil {
			if db, err = refdb.Open(settingsDB); err != nil {
				return nil, err
			}
			defer db.Close()
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(sym string) {
		if sym != "" && !seen[sym] {
			seen[sym] = true
			out = append(out, sym)
		}
	}
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if db != nil {
			list, ok, err := refdb.SymbolList(db, strings.ToLower(tok))
			if err != nil {
				return nil, err
			}
			if ok {
				for _, sym := range list {
					add(sym)
				}
				continue
			}
		}
		add(strings.ToUpper(tok))
	}
	return out, nil
}

// Config holds every setting of a download run. Zero values mean "unset";
// DefaultConfig returns the CLI defaults.
type Config struct {
	DB, SettingsDB, Source, PolygonKey  string
	AlpacaKey, AlpacaSecret, AlpacaFeed string // alpaca source: key id, secret, feed (sip or iex)
	Symbols, Table                      string
	Limit, Years                        int
	Start, End, Timeframe, TargetTable  string
	Force                               bool
	UniverseDB                          string // universe.db read by UnleveragedETFs
	UnleveragedETFs                     bool   // symbols = every active ETF with no leverage, most traded first
	Status                              bool   // only report what the target database holds and what is left to pull
	Concurrency                         int
	OTM                                 string    // polygon-options: comma-separated % OTM
	Rate                                int       // polygon: API calls per minute, 0 = unthrottled
	MaxCalls                            int       // polygon-options
	Out                                 io.Writer // progress output; nil discards
}

// Summary reports what a Run did.
type Summary struct {
	Symbols, CachedSymbols, UpdatedSymbols, FailedSymbols, NewBars int
}

// DefaultConfig returns the same defaults the market_history CLI uses.
func DefaultConfig() Config {
	return Config{
		DB: appenv.MarketDB(), SettingsDB: appenv.RefDB(), UniverseDB: appenv.UniverseDB(), Source: "alpaca",
		Table: "leveraged_etf", Limit: 50, Years: 5, Timeframe: "1h",
		TargetTable: "backtest_start",
		Concurrency: runtime.NumCPU(), OTM: "0,2,5", Rate: 5,
	}
}

// bindFlags registers the download flags on cfg, with d as the defaults.
func bindFlags(cfg *Config, d Config) {
	flag.StringVar(&cfg.DB, "db", d.DB, "Hourly market history DB (default: APP_FOLDER/data/market_history_hourly.db); the daily sessions that signals read are derived into the same table")
	flag.StringVar(&cfg.SettingsDB, "settings", d.SettingsDB, "Reference DB path (strategies and symbol tables)")
	flag.StringVar(&cfg.Source, "source", d.Source, "Data source provider: alpaca (default), polygon, polygon-options or alpaca-options (option end-of-day bars into option_contracts / option_bars)")
	flag.StringVar(&cfg.AlpacaKey, "alpaca-key", d.AlpacaKey, "Alpaca key id (or set ALPACA_KEY in environment or .env)")
	flag.StringVar(&cfg.AlpacaSecret, "alpaca-secret", d.AlpacaSecret, "Alpaca secret (or set ALPACA_SECRET in environment or .env)")
	flag.StringVar(&cfg.AlpacaFeed, "alpaca-feed", d.AlpacaFeed, "(alpaca) data feed: sip (consolidated, default; needs a paid data plan) or iex (free plan, one exchange); default ALPACA_FEED or sip")
	flag.StringVar(&cfg.PolygonKey, "polygon-key", d.PolygonKey, "Polygon.io API key (or set POLYGON_API_KEY in environment or .env)")
	flag.StringVar(&cfg.Symbols, "symbols", d.Symbols, "Comma-separated symbols to download (e.g. SPY,QQQ), or ids of symbol_lists rows in the settings DB (e.g. etf-pre-2021-unleveraged). Bare arguments work too: market_history VOO, IEF, GLD")
	flag.StringVar(&cfg.Table, "table", d.Table, "Table name in strategies.db with symbols (fallback if no symbols specified)")
	flag.IntVar(&cfg.Limit, "limit", d.Limit, "Limit number of symbols (0 for all)")
	flag.IntVar(&cfg.Years, "years", d.Years, "Number of years of history")
	flag.StringVar(&cfg.Start, "start", d.Start, "Optional start date (YYYY-MM-DD), overrides -years")
	flag.StringVar(&cfg.End, "end", d.End, "Optional end date (YYYY-MM-DD), defaults to now")
	flag.StringVar(&cfg.Timeframe, "timeframe", d.Timeframe, "Bar timeframe: only 1h (daily sessions are derived from the hourly bars)")
	flag.StringVar(&cfg.TargetTable, "target-table", d.TargetTable, "Target table name in target SQLite DB")
	flag.StringVar(&cfg.UniverseDB, "universe-db", d.UniverseDB, "Universe database read by -unleveraged-etfs")
	flag.BoolVar(&cfg.UnleveragedETFs, "unleveraged-etfs", d.UnleveragedETFs, "Download every active ETF with no leverage from the universe database, most traded first (use with -source polygon -timeframe 1h; hourly history is 2 years on the free tier, so -years defaults to 2 here)")
	flag.BoolVar(&cfg.Status, "status", d.Status, "Do not download: summarize which bars of the symbol list are already in the target database and what is left to pull")
	flag.BoolVar(&cfg.Force, "force", d.Force, "Force re-downloading all bars even if already present in database")
	flag.IntVar(&cfg.Concurrency, "concurrency", d.Concurrency, "Number of symbols to fetch concurrently (network-bound; DB writes are serialized internally). Defaults to all CPU cores.")
	flag.StringVar(&cfg.OTM, "otm", d.OTM, "(polygon-options) call strikes to pull, as % OTM vs spot at the roll date (nearest listed strike each)")
	flag.IntVar(&cfg.Rate, "rate", d.Rate, "(polygon, polygon-options) API calls per minute; 5 = Polygon free tier, 0 = unthrottled (paid). A rate-limited (429) call waits a minute and retries")
	flag.IntVar(&cfg.MaxCalls, "max-calls", d.MaxCalls, "(polygon-options) stop after this many API calls (0 = no cap); reruns resume, finished contracts are skipped")
}

// resolveKeys fills the provider keys from the environment or .env. CLI only: Run never reads the environment.
func resolveKeys(cfg *Config) {
	if strings.TrimSpace(cfg.PolygonKey) == "" {
		cfg.PolygonKey = datasource.ResolvePolygonAPIKey() // POLYGON_API_KEY / .env: CLI only, Run never reads the environment
	}
	if strings.TrimSpace(cfg.AlpacaKey) == "" || strings.TrimSpace(cfg.AlpacaSecret) == "" {
		cfg.AlpacaKey, cfg.AlpacaSecret = datasource.ResolveAlpacaKeys() // ALPACA_KEY / ALPACA_SECRET / .env: CLI only
	}
	if strings.TrimSpace(cfg.AlpacaFeed) == "" {
		cfg.AlpacaFeed = appenv.Get("ALPACA_FEED")
	}
}

// Main is the CLI entry point.
func Main() {
	d := DefaultConfig()
	cfg := d
	sub := cliutils.PopSubcommand(map[string]string{"update": "update", "sessions": "sessions"})
	if sub == "update" {
		updateMain()
		return
	}
	if sub == "sessions" {
		sessionsMain()
		return
	}
	bindFlags(&cfg, d)
	flag.Parse()
	if cfg.UnleveragedETFs {
		set := false
		flag.Visit(func(f *flag.Flag) { set = set || f.Name == "years" || f.Name == "start" })
		if !set {
			cfg.Years = 2
		}
	}
	// ./bin/market_history VOO, IEF, GLD  — the shell splits on spaces, so each
	// ticker arrives as its own arg, often with a trailing comma.
	if strings.TrimSpace(cfg.Symbols) == "" {
		if syms := parseSymbolArgs(flag.Args()); len(syms) > 0 {
			cfg.Symbols = strings.Join(syms, ",")
		}
	}
	cfg.Out = os.Stdout
	if !isOptionSource(cfg.Source) {
		if err := appenv.RequireHourly(cfg.DB); err != nil {
			log.Fatal(err)
		}
	}
	resolveKeys(&cfg)
	if _, err := Run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

// missingFetches lists the date windows of sym that cov (what the target
// database holds) does not cover, for the requested start..end. Nothing is
// listed when the database already has the whole window.
func missingFetches(sym string, cov storage.SymbolDateCoverage, start, end time.Time, timeframe string, force bool) []datasource.FetchRequest {
	reqStartStr, reqEndStr := start.Format("2006-01-02"), end.Format("2006-01-02")
	if cov.BarCount == 0 || force {
		// Symbol is completely missing from DB (or forced full refresh)
		return []datasource.FetchRequest{{Symbol: sym, StartDate: start, EndDate: end, Timeframe: timeframe}}
	}
	var out []datasource.FetchRequest
	// Older bars missing: start is before cov.MinDate by more than a weekend/holiday.
	if reqStartStr < cov.MinDate {
		dbMin, err := time.Parse("2006-01-02", cov.MinDate)
		if err == nil && dbMin.Sub(start) > 4*24*time.Hour {
			out = append(out, datasource.FetchRequest{Symbol: sym, StartDate: start, EndDate: dbMin, Timeframe: timeframe})
		}
	}
	// Newer bars missing: end is after cov.MaxDate.
	if cov.MaxDate < reqEndStr {
		dbMax, err := time.Parse("2006-01-02", cov.MaxDate)
		// A daily series wants the latest sessions. An intraday series stops at
		// the last closed session, so a few trailing days (weekend, holiday, the
		// session still open) are not a gap worth a request.
		stale := cov.MaxDate != reqEndStr && (timeframe == "1d" || end.Sub(dbMax) > 4*24*time.Hour)
		if err == nil && stale {
			out = append(out, datasource.FetchRequest{Symbol: sym, StartDate: dbMax, EndDate: end, Timeframe: timeframe})
		}
	}
	return out
}

// syncWriter serialises writes so concurrent workers can share one io.Writer.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Run downloads or imports market history per cfg. It uses only cfg (no
// environment variables, .env, or app-folder paths), returns errors rather
// than exiting, writes progress to cfg.Out (nil discards), and stops when ctx
// is cancelled. Empty Table/Timeframe/Source/etc. fall back to the CLI
// defaults; DB must be set, and polygon sources need PolygonKey.
func Run(ctx context.Context, cfg Config) (*Summary, error) {
	var out io.Writer = io.Discard
	if cfg.Out != nil {
		out = &syncWriter{w: cfg.Out}
	}
	if cfg.DB == "" {
		return nil, fmt.Errorf("market_history: DB is required")
	}
	if cfg.TargetTable == "" {
		cfg.TargetTable = "backtest_start"
	}
	if cfg.Timeframe == "" {
		cfg.Timeframe = "1h"
	}
	if cfg.Source == "" {
		cfg.Source = "alpaca"
	}
	if !isOptionSource(cfg.Source) {
		if tf := strings.ToLower(strings.TrimSpace(cfg.Timeframe)); tf != "1h" && tf != "h" {
			return nil, fmt.Errorf("market_history: timeframe %q is not supported: only hourly bars (-timeframe 1h) are downloaded, and daily sessions are derived from them", cfg.Timeframe)
		}
		if src := strings.ToLower(cfg.Source); src != "alpaca" && src != "polygon" {
			return nil, fmt.Errorf("market_history: source %q is not supported for hourly bars: use alpaca or polygon", cfg.Source)
		}
	}
	// Hourly bars and the sessions derived from them share one database.
	cfg.DB = appenv.BarDB(cfg.DB, cfg.Timeframe)
	if cfg.Years <= 0 {
		cfg.Years = 4
	}
	if cfg.OTM == "" {
		cfg.OTM = "0,2,5"
	}
	if src := strings.ToLower(cfg.Source); (src == "polygon" || src == "polygon-options") && strings.TrimSpace(cfg.PolygonKey) == "" && !cfg.Status {
		return nil, fmt.Errorf("market_history: source %s requires Config.PolygonKey", src)
	}

	if src := strings.ToLower(cfg.Source); (src == "alpaca" || src == "alpaca-options") && (strings.TrimSpace(cfg.AlpacaKey) == "" || strings.TrimSpace(cfg.AlpacaSecret) == "") && !cfg.Status {
		return nil, fmt.Errorf("market_history: source alpaca requires Config.AlpacaKey and Config.AlpacaSecret")
	}

	db, err := storage.OpenSQLite(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("Failed to open target DB %s: %v", cfg.DB, err)
	}
	defer db.Close()

	if err := storage.EnsureBarTable(db, cfg.TargetTable); err != nil {
		return nil, fmt.Errorf("Failed to initialize database table schema: %v", err)
	}

	// 0. Option history (Polygon free tier) for one underlying.
	if strings.ToLower(cfg.Source) == "polygon-options" {
		underlying := "VOO"
		if syms := strings.Split(cfg.Symbols, ","); strings.TrimSpace(syms[0]) != "" {
			underlying = strings.ToUpper(strings.TrimSpace(syms[0]))
		}
		otms, err := parseOTMList(cfg.OTM)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}
		end := time.Now().UTC()
		start := end.AddDate(-2, 0, 7) // free tier: ~2 years of option history
		if cfg.Start != "" {
			if start, err = time.Parse("2006-01-02", cfg.Start); err != nil {
				return nil, fmt.Errorf("Invalid -start date %q: expected YYYY-MM-DD", cfg.Start)
			}
		}
		if cfg.End != "" {
			if end, err = time.Parse("2006-01-02", cfg.End); err != nil {
				return nil, fmt.Errorf("Invalid -end date %q: expected YYYY-MM-DD", cfg.End)
			}
		}
		if err := downloadOptionHistory(ctx, out, db, cfg.TargetTable, underlying, start, end, otms, cfg.PolygonKey, cfg.Rate, cfg.MaxCalls); err != nil {
			return nil, fmt.Errorf("option download: %v", err)
		}
		return &Summary{}, nil
	}

	// 0b. Option history from Alpaca (calls and puts, expired included, daily bars).
	if strings.ToLower(cfg.Source) == "alpaca-options" {
		var underlyings []string
		for _, sym := range strings.Split(cfg.Symbols, ",") {
			if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
				underlyings = append(underlyings, sym)
			}
		}
		if len(underlyings) == 0 {
			return nil, fmt.Errorf("market_history: -source alpaca-options needs -symbols (the underlyings, e.g. UPRO,SPXU)")
		}
		end := time.Now().UTC()
		start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // Alpaca option history starts in early 2024
		var perr error
		if cfg.Start != "" {
			if start, perr = time.Parse("2006-01-02", cfg.Start); perr != nil {
				return nil, fmt.Errorf("Invalid -start date %q: expected YYYY-MM-DD", cfg.Start)
			}
		}
		if cfg.End != "" {
			if end, perr = time.Parse("2006-01-02", cfg.End); perr != nil {
				return nil, fmt.Errorf("Invalid -end date %q: expected YYYY-MM-DD", cfg.End)
			}
		}
		if err := downloadAlpacaOptions(ctx, out, db, underlyings, start, end, cfg.AlpacaKey, cfg.AlpacaSecret); err != nil {
			return nil, fmt.Errorf("option download: %v", err)
		}
		return &Summary{}, nil
	}

	// Resolve symbol list
	var symbols []string
	if cfg.Symbols != "" {
		var err error
		if symbols, err = expandSymbolLists(cfg.SettingsDB, strings.Split(cfg.Symbols, ",")); err != nil {
			return nil, fmt.Errorf("Failed to read symbol_lists from %s: %v", cfg.SettingsDB, err)
		}
	} else if cfg.UnleveragedETFs {
		udb, err := universe.Open(cfg.UniverseDB)
		if err != nil {
			return nil, fmt.Errorf("Failed to open universe db %s: %v", cfg.UniverseDB, err)
		}
		symbols, err = universe.UnleveragedETFs(udb)
		udb.Close()
		if err != nil {
			return nil, fmt.Errorf("Failed to list unleveraged ETFs from %s: %v", cfg.UniverseDB, err)
		}
	} else {
		dbSymbols, err := getSymbolsFromTable(cfg.SettingsDB, cfg.Table, cfg.Limit)
		if err != nil {
			fmt.Fprintf(out, "Warning: failed to query strategies.db table %s: %v\n", cfg.Table, err)
		}
		symbols = dbSymbols
	}

	if len(symbols) == 0 {
		return nil, fmt.Errorf("No symbols resolved. Pass tickers (market_history VOO, IEF, GLD), -symbols SPY,QQQ or a symbol_lists id, -unleveraged-etfs, or -table <name>")
	}

	fmt.Fprintf(out, "Database: %s (Table: %s)\n", cfg.DB, cfg.TargetTable)
	fmt.Fprintf(out, "Target Universe: %d symbols: %v\n", len(symbols), symbols)

	client := &http.Client{Timeout: 15 * time.Second}
	var primarySource datasource.DataSource

	switch strings.ToLower(cfg.Source) {
	case "polygon":
		polySource := datasource.NewPolygonDataSource(cfg.PolygonKey, client)
		if polySource.APIKey == "" && !cfg.Status {
			return nil, fmt.Errorf("Polygon API key is required. Pass -polygon-key <KEY> or set POLYGON_API_KEY in your environment / .env file")
		}
		polySource.SetRate(cfg.Rate)
		primarySource = polySource
	default: // alpaca
		primarySource = datasource.NewAlpacaDataSource(cfg.AlpacaKey, cfg.AlpacaSecret, cfg.AlpacaFeed, client)
	}

	now := time.Now().UTC()
	start := now.AddDate(-cfg.Years, 0, 0)
	end := now

	if cfg.Start != "" {
		if parsed, err := time.Parse("2006-01-02", cfg.Start); err == nil {
			start = parsed.UTC()
		} else {
			return nil, fmt.Errorf("Invalid -start date %q: expected YYYY-MM-DD", cfg.Start)
		}
	}
	if cfg.End != "" {
		if parsed, err := time.Parse("2006-01-02", cfg.End); err == nil {
			end = parsed.UTC()
		} else {
			return nil, fmt.Errorf("Invalid -end date %q: expected YYYY-MM-DD", cfg.End)
		}
	}

	reqStartStr := start.Format("2006-01-02")
	reqEndStr := end.Format("2006-01-02")

	if cfg.Status {
		return reportStatus(out, db, cfg, symbols, start, end)
	}

	fmt.Fprintf(out, "Target Horizon: %s bars from %s to %s using %s\n",
		cfg.Timeframe, reqStartStr, reqEndStr, primarySource.Name())
	fmt.Fprintf(out, "🔍 Checking %s first for existing cached data...\n\n", filepath.Base(cfg.DB))

	totalNewBars := 0
	cachedSymbols := 0
	updatedSymbols := 0
	failedSymbols := 0

	var mu sync.Mutex // serializes all DB access and shared counters/printing (SQLite allows only one writer at a time)

	processSymbol := func(idx int, sym string) {
		if ctx.Err() != nil {
			return
		}
		mu.Lock()
		cov, err := storage.GetSymbolDateCoverageWithTimeframe(db, cfg.TargetTable, sym, cfg.Timeframe)
		mu.Unlock()
		if err != nil {
			fmt.Fprintf(out, "[%d/%d] Error checking database coverage for %s: %v\n", idx+1, len(symbols), sym, err)
		}

		missingWindows := missingFetches(sym, cov, start, end, cfg.Timeframe, cfg.Force)

		// If DB already has all requested data, skip remote fetch!
		if len(missingWindows) == 0 {
			mu.Lock()
			cachedSymbols++
			fmt.Fprintf(out, "[%d/%d] %-6s : ⚡ Up-to-date in %s (%d bars, %s ➔ %s). 0 missing, skipped remote fetch.\n",
				idx+1, len(symbols), sym, filepath.Base(cfg.DB), cov.BarCount, cov.MinDate, cov.MaxDate)
			mu.Unlock()
			return
		}

		// Fetch only the missing window(s) — network I/O happens outside the lock so
		// multiple symbols can be in flight concurrently.
		symNewBars := 0
		fetchError := false

		for _, winReq := range missingWindows {
			bars, err := primarySource.Fetch(ctx, winReq)
			if err != nil {
				fmt.Fprintf(out, "[%d/%d] Failed fetching missing data for %s (%s to %s): %v\n",
					idx+1, len(symbols), sym, winReq.StartDate.Format("2006-01-02"), winReq.EndDate.Format("2006-01-02"), err)
				fetchError = true
				continue
			}
			if len(bars) == 0 {
				continue
			}

			mu.Lock()
			err = storage.UpsertBars(db, cfg.TargetTable, bars)
			mu.Unlock()
			if err != nil {
				fmt.Fprintf(out, "[%d/%d] Error saving %s bars to DB: %v\n", idx+1, len(symbols), sym, err)
				fetchError = true
				continue
			}
			symNewBars += len(bars)
		}

		mu.Lock()
		defer mu.Unlock()

		if fetchError && symNewBars == 0 && cov.BarCount == 0 {
			failedSymbols++
			return
		}

		totalNewBars += symNewBars
		updatedSymbols++

		if cov.BarCount == 0 {
			fmt.Fprintf(out, "[%d/%d] %-6s : 📥 Not in DB. Pulled %d bars (%s ➔ %s) from %s ➔ %s\n",
				idx+1, len(symbols), sym, symNewBars, reqStartStr, reqEndStr, primarySource.Name(), filepath.Base(cfg.DB))
		} else if symNewBars > 0 && cfg.Force {
			fmt.Fprintf(out, "[%d/%d] %-6s : 🔄 Found %d bars in DB (%s ➔ %s). Refreshed the whole window (%d bars) from %s ➔ %s\n",
				idx+1, len(symbols), sym, cov.BarCount, cov.MinDate, cov.MaxDate, symNewBars, primarySource.Name(), filepath.Base(cfg.DB))
		} else if symNewBars > 0 {
			fmt.Fprintf(out, "[%d/%d] %-6s : 🔄 Found %d bars in DB (%s ➔ %s). Pulled %d missing bars from %s ➔ %s\n",
				idx+1, len(symbols), sym, cov.BarCount, cov.MinDate, cov.MaxDate, symNewBars, primarySource.Name(), filepath.Base(cfg.DB))
		} else {
			fmt.Fprintf(out, "[%d/%d] %-6s : ⚡ Found %d bars in DB (%s ➔ %s). Checked %s (no new closed bars available).\n",
				idx+1, len(symbols), sym, cov.BarCount, cov.MinDate, cov.MaxDate, primarySource.Name())
		}
	}

	workers := cfg.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers == 1 {
		for idx, sym := range symbols {
			processSymbol(idx, sym)
			select { // gentle rate limit for serial mode
			case <-ctx.Done():
			case <-time.After(120 * time.Millisecond):
			}
		}
	} else {
		fmt.Fprintf(out, "🚀 Downloading with %d concurrent workers...\n\n", workers)
		jobs := make(chan int, len(symbols))
		for idx := range symbols {
			jobs <- idx
		}
		close(jobs)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					processSymbol(idx, symbols[idx])
				}
			}()
		}
		wg.Wait()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("market_history cancelled: %w", err)
	}

	if n, err := sessions.Derive(db, cfg.TargetTable); err != nil {
		fmt.Fprintf(out, "\n⚠️  derive sessions failed: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(out, "\n📆 Derived %d session bars (daily rows read by signals) from the hourly bars.\n", n)
	}
	fmt.Fprintf(out, "\n✨ Summary: %s is updated! (%d symbols already up-to-date, %d symbols fetched/updated, %d failed, %d new bars added).\n   %d requested symbols processed against %s.\n",
		cfg.DB, cachedSymbols, updatedSymbols, failedSymbols, totalNewBars, len(symbols), filepath.Base(cfg.DB))
	return &Summary{Symbols: len(symbols), CachedSymbols: cachedSymbols, UpdatedSymbols: updatedSymbols, FailedSymbols: failedSymbols, NewBars: totalNewBars}, nil
}

// reportStatus prints, for the requested symbols and window, how many are
// already complete in the target database and what is left to pull, with the
// request count and the time that takes at cfg.Rate. It makes no request.
func reportStatus(out io.Writer, db *sqlx.DB, cfg Config, symbols []string, start, end time.Time) (*Summary, error) {
	sum := &Summary{Symbols: len(symbols)}
	var held, requests, partial int
	var left []string
	for _, sym := range symbols {
		cov, err := storage.GetSymbolDateCoverageWithTimeframe(db, cfg.TargetTable, sym, cfg.Timeframe)
		if err != nil {
			return nil, err
		}
		held += cov.BarCount
		wins := missingFetches(sym, cov, start, end, cfg.Timeframe, cfg.Force)
		switch {
		case len(wins) == 0:
			sum.CachedSymbols++
		case cov.BarCount == 0:
			left = append(left, sym)
		default:
			partial++
			left = append(left, sym)
		}
		requests += len(wins)
	}
	sum.UpdatedSymbols = len(left)
	fmt.Fprintf(out, "%s bars %s to %s in %s (table %s)\n", cfg.Timeframe, start.Format("2006-01-02"), end.Format("2006-01-02"), filepath.Base(cfg.DB), cfg.TargetTable)
	fmt.Fprintf(out, "  symbols requested : %d\n", len(symbols))
	fmt.Fprintf(out, "  already pulled    : %d (%d bars held for the list)\n", sum.CachedSymbols, held)
	fmt.Fprintf(out, "  partly pulled     : %d (missing the older or the newer days)\n", partial)
	fmt.Fprintf(out, "  not pulled        : %d\n", len(left)-partial)
	fmt.Fprintf(out, "  left to pull      : %d symbols, at least %d requests", len(left), requests)
	if cfg.Rate > 0 && requests > 0 {
		eta := time.Duration(requests) * time.Minute / time.Duration(cfg.Rate)
		fmt.Fprintf(out, ", about %s at %d calls/min", eta.Round(time.Minute), cfg.Rate)
	}
	fmt.Fprintln(out)
	if len(left) > 0 {
		show := left
		if len(show) > 15 {
			show = show[:15]
		}
		fmt.Fprintf(out, "  next up           : %s", strings.Join(show, " "))
		if len(left) > len(show) {
			fmt.Fprintf(out, " ... (+%d more)", len(left)-len(show))
		}
		fmt.Fprintln(out)
	}
	return sum, nil
}

// isOptionSource reports whether source downloads option history, which lives in
// option_contracts / option_bars and needs neither hourly bars nor the hourly timeframe.
func isOptionSource(source string) bool {
	src := strings.ToLower(source)
	return src == "polygon-options" || src == "alpaca-options"
}

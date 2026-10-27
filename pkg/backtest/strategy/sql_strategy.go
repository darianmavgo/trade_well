package strategy

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Placeholders substituted with the strategy's own StrategyConfig values in
// any pipeline SQL file, so a strategy's take-profit/stop-loss/hold-days
// (and, for two-leg combos, its short-leg equivalents) are real tunable
// parameters instead of hardcoded literals baked into the .sql file — the
// same problem __DECLINE_DAYS__ solves for the consecutive decline/rally-day
// window (e.g. sql/strategies/streak_strategy).
const (
	declineDaysPlaceholder         = "__DECLINE_DAYS__"
	takeProfitMultPlaceholder      = "__TAKE_PROFIT_MULT__"
	stopLossMultPlaceholder        = "__STOP_LOSS_MULT__"
	holdDaysPlaceholder            = "__HOLD_DAYS__"
	shortTakeProfitMultPlaceholder = "__SHORT_TAKE_PROFIT_MULT__"
	shortStopLossMultPlaceholder   = "__SHORT_STOP_LOSS_MULT__"
	shortHoldDaysPlaceholder       = "__SHORT_HOLD_DAYS__"
	// symbolPlaceholder is substituted from StrategyConfig.Benchmark, letting one
	// pipeline directory (e.g. sql/strategies/streak_strategy) serve many
	// per-symbol strategy instances instead of needing one directory per ticker.
	symbolPlaceholder = "__SYMBOL__"
	// streak_strategy pipeline: watch symbol, bought symbol, streak column,
	// and the regime filter. Values are whitelisted; a bad row is left unsubstituted.
	signalSymbolPlaceholder    = "__SIGNAL_SYMBOL__"
	tradeSymbolPlaceholder     = "__TRADE_SYMBOL__"
	streakColPlaceholder       = "__STREAK_COL__"
	regimePredicatePlaceholder = "__REGIME_PREDICATE__"
	regimeLabelPlaceholder     = "__REGIME_LABEL__"
)

const directionPlaceholder = "__DIRECTION__"

// SignalDirection maps a strategy row's direction ("long", "short") to the value a signal
// carries: "SHORT" for a short row and "LONG" for everything else.
func SignalDirection(rowDirection string) string {
	if strings.EqualFold(strings.TrimSpace(rowDirection), "short") {
		return "SHORT"
	}
	return "LONG"
}

// streakSymbolRe is the ticker whitelist for SQL substitution. A row that
// fails it is not written into the query.
var streakSymbolRe = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,9}$`)

// StreakSymbol reports whether s is safe to substitute into a SQL literal.
// The returned symbol is trimmed and uppercased.
func StreakSymbol(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !streakSymbolRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// StreakColumn maps a streak direction to the slice column the pipeline filters.
func StreakColumn(direction string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "drop":
		return "down_streak", true
	case "rally":
		return "up_streak", true
	default:
		return "", false
	}
}

// RegimePredicate maps a regime label to the watch-bar filter used by
// sql/strategies/streak_strategy (the slice is aliased as v). SMA200 == 0
// does not filter, matching the gridsearch streak entries stage. SMA50 is not
// supported here.
func RegimePredicate(signalSymbol, regime string) (string, bool) {
	sym, ok := StreakSymbol(signalSymbol)
	if !ok {
		return "", false
	}
	regime = strings.TrimSpace(regime)
	switch {
	case regime == "All Regimes":
		return "1=1", true
	case regime == sym+">=SMA200":
		return "v.sma200 <= 0 OR v.close >= v.sma200", true
	case regime == sym+"<SMA200":
		return "v.sma200 <= 0 OR v.close < v.sma200", true
	case regime == "bull" || regime == "bear" || regime == "sideways":
		return "1=1", true // Predicate is handled externally by markov generator
	default:
		return "", false
	}
}

// takeProfitMultiplier converts a StrategyConfig take-profit *offset* (e.g.
// TakeProfitPct/ShortTakeProfitPct = 0.08 for +8%) into the multiplier a SQL
// pipeline should apply to an entry price, preserving the 0.0 "no per-signal
// override" sentinel the simulator checks for (pkg/simulator/portfolio.go:
// `if sig.TakeProfit > 0`, falling back to config-level TargetPct/
// TakeProfitPct) when a strategy hasn't set that side at all.
func takeProfitMultiplier(offsetPct float64) float64 {
	if offsetPct <= 0 {
		return 0.0
	}
	return 1.0 + offsetPct
}

// stopLossMultiplier passes a StrategyConfig stop-loss *multiplier* (e.g.
// StopLossPct/ShortStopLossPct = 0.98 for -2%, applied directly as
// entryPrice*multiplier — see StrategyConfig.StopLossPct's doc comment)
// straight through to SQL, preserving the same 0.0 "no per-signal override"
// sentinel as takeProfitMultiplier.
func stopLossMultiplier(multiplier float64) float64 {
	if multiplier <= 0 {
		return 0.0
	}
	return multiplier
}

// substitutePlaceholders replaces every strategy-config placeholder present
// in sqlText. Only __DECLINE_DAYS__ has a failure mode worth warning about —
// 0.0 is a legitimate "no override" value for every TP/SL/hold placeholder,
// but a decline/rally window of 0 days produces a broken WHERE clause.
func substitutePlaceholders(sqlText, id, fileName string, cfg StrategyConfig) string {
	if strings.Contains(sqlText, declineDaysPlaceholder) {
		if cfg.DeclineDays <= 0 {
			log.Printf("[sql_strategy %s] %s references %s but config.DeclineDays is unset — leaving query unsubstituted, it will fail", id, fileName, declineDaysPlaceholder)
		} else {
			sqlText = strings.ReplaceAll(sqlText, declineDaysPlaceholder, strconv.Itoa(cfg.DeclineDays))
		}
	}
	sqlText = strings.ReplaceAll(sqlText, takeProfitMultPlaceholder, strconv.FormatFloat(takeProfitMultiplier(cfg.TakeProfitPct), 'f', 6, 64))
	sqlText = strings.ReplaceAll(sqlText, stopLossMultPlaceholder, strconv.FormatFloat(stopLossMultiplier(cfg.StopLossPct), 'f', 6, 64))
	sqlText = strings.ReplaceAll(sqlText, holdDaysPlaceholder, strconv.Itoa(cfg.HoldingWindow))
	sqlText = strings.ReplaceAll(sqlText, shortTakeProfitMultPlaceholder, strconv.FormatFloat(takeProfitMultiplier(cfg.ShortTakeProfitPct), 'f', 6, 64))
	sqlText = strings.ReplaceAll(sqlText, shortStopLossMultPlaceholder, strconv.FormatFloat(stopLossMultiplier(cfg.ShortStopLossPct), 'f', 6, 64))
	sqlText = strings.ReplaceAll(sqlText, shortHoldDaysPlaceholder, strconv.Itoa(cfg.ShortHoldingWindow))
	if strings.Contains(sqlText, symbolPlaceholder) {
		if cfg.Benchmark == "" {
			log.Printf("[sql_strategy %s] %s references %s but config.Benchmark is unset — leaving query unsubstituted, it will fail", id, fileName, symbolPlaceholder)
		} else {
			sqlText = strings.ReplaceAll(sqlText, symbolPlaceholder, cfg.Benchmark)
		}
	}
	sqlText = substituteStreakPlaceholders(sqlText, id, fileName, cfg)
	// The markov pipelines stamp each entry with the row's side. A signal left as the literal
	// placeholder is read by the live scan as neither LONG nor BUY and is dropped, so every
	// markov entry would be reported NO_SIGNAL.
	sqlText = strings.ReplaceAll(sqlText, directionPlaceholder, SignalDirection(cfg.StreakDirection))
	// Trained-model databases the markov pipelines attach (read only).
	sqlText = strings.ReplaceAll(sqlText, "__MARKOV_DB__", appenv.MarkovDB())
	sqlText = strings.ReplaceAll(sqlText, "__TREE_DB__", appenv.TreeDB())
	sqlText = strings.ReplaceAll(sqlText, "__HMM_DB__", appenv.ReportFile("hmm_regime.db"))
	// The hidden Markov model database written by train hmm (the hmm family reads it).
	sqlText = strings.ReplaceAll(sqlText, "__HMM_MODEL_DB__", appenv.HMMDB())

	for k, v := range cfg.SQLParams {
		sqlText = strings.ReplaceAll(sqlText, "__"+k+"__", v)
	}

	sqlText = strings.ReplaceAll(sqlText, "__COIL_MAX__", strconv.FormatFloat(cfg.TreeCoilMax, 'f', -1, 64))
	sqlText = strings.ReplaceAll(sqlText, "__SMA_MIN__", strconv.FormatFloat(cfg.TreeSMAMin, 'f', -1, 64))
	sqlText = strings.ReplaceAll(sqlText, "__SMA_MAX__", strconv.FormatFloat(cfg.TreeSMAMax, 'f', -1, 64))

	return sqlText
}

// substituteStreakPlaceholders fills the streak_strategy pipeline placeholders.
// A value that fails the whitelist is left in place so the query fails closed
// instead of running injected SQL.
func substituteStreakPlaceholders(sqlText, id, fileName string, cfg StrategyConfig) string {
	if strings.Contains(sqlText, signalSymbolPlaceholder) {
		if sym, ok := StreakSymbol(cfg.Benchmark); ok {
			sqlText = strings.ReplaceAll(sqlText, signalSymbolPlaceholder, sym)
		} else {
			log.Printf("[sql_strategy %s] %s %s rejected %q", id, fileName, signalSymbolPlaceholder, cfg.Benchmark)
		}
	}
	if strings.Contains(sqlText, tradeSymbolPlaceholder) {
		if sym, ok := StreakSymbol(cfg.TradeSymbol); ok {
			sqlText = strings.ReplaceAll(sqlText, tradeSymbolPlaceholder, sym)
		} else {
			log.Printf("[sql_strategy %s] %s %s rejected %q", id, fileName, tradeSymbolPlaceholder, cfg.TradeSymbol)
		}
	}
	if strings.Contains(sqlText, streakColPlaceholder) {
		if col, ok := StreakColumn(cfg.StreakDirection); ok {
			sqlText = strings.ReplaceAll(sqlText, streakColPlaceholder, col)
		} else {
			log.Printf("[sql_strategy %s] %s %s rejected %q", id, fileName, streakColPlaceholder, cfg.StreakDirection)
		}
	}
	// Predicate before the label so the longer token is not partially consumed.
	if strings.Contains(sqlText, regimePredicatePlaceholder) {
		if pred, ok := RegimePredicate(cfg.Benchmark, cfg.Regime); ok {
			sqlText = strings.ReplaceAll(sqlText, regimePredicatePlaceholder, pred)
		} else {
			log.Printf("[sql_strategy %s] %s %s rejected %q", id, fileName, regimePredicatePlaceholder, cfg.Regime)
		}
	}
	if strings.Contains(sqlText, regimeLabelPlaceholder) {
		if _, ok := RegimePredicate(cfg.Benchmark, cfg.Regime); ok {
			sqlText = strings.ReplaceAll(sqlText, regimeLabelPlaceholder, strings.TrimSpace(cfg.Regime))
		} else {
			log.Printf("[sql_strategy %s] %s %s rejected %q", id, fileName, regimeLabelPlaceholder, cfg.Regime)
		}
	}
	return sqlText
}

// SQLPipelineStrategy adapts any directory of sequential SQL scripts into a runnable Strategy.
type SQLPipelineStrategy struct {
	id           string
	name         string
	description  string
	pipelineDir  string
	marketDBPath string
	calcDBPath   string
	config       StrategyConfig
}

// NewSQLPipeline builds a pipeline without registering it. Use this when the
// pipeline is an implementation detail of another strategy (streak_strategy
// runs one shared directory per row and must not add a second registry id).
func NewSQLPipeline(id, name, description, pipelineDir string, config StrategyConfig) *SQLPipelineStrategy {
	config.ID = id
	config.Name = name
	config.Description = description
	return &SQLPipelineStrategy{
		id:          id,
		name:        name,
		description: description,
		pipelineDir: pipelineDir,
		config:      config,
	}
}

// NewSQLPipelineStrategy creates a new SQL-backed strategy from a directory of SQL scripts
// and registers it.
func NewSQLPipelineStrategy(id, name, description, pipelineDir string, config StrategyConfig) *SQLPipelineStrategy {
	s := NewSQLPipeline(id, name, description, pipelineDir, config)
	Register(s)
	return s
}

func (s *SQLPipelineStrategy) ID() string {
	return s.id
}

func (s *SQLPipelineStrategy) Name() string {
	return s.name
}

func (s *SQLPipelineStrategy) Description() string {
	return s.description
}

func (s *SQLPipelineStrategy) DefaultConfig() StrategyConfig {
	return s.config
}

// PipelineDir returns the directory of SQL scripts this pipeline executes,
// resolved however it was originally constructed (e.g. AutoRegisterSQLStrategies
// resolves it relative to a caller-supplied root, which differs between
// production binaries run from the repo root and tests run from a package
// directory). Lets other strategies that build their own one-off pipeline
// instance (to inject a per-instance config) reuse the already-correct path
// instead of hardcoding one that only works from the repo root.
func (s *SQLPipelineStrategy) PipelineDir() string {
	return s.pipelineDir
}

func (s *SQLPipelineStrategy) Validate() error {
	return ValidateConfig(s.config)
}

// SetDatabases injects the required database paths.
func (s *SQLPipelineStrategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// RequiredSymbols inspects the pipeline SQL queries for explicit symbol requirements (e.g. symbol = 'XYZ').
func (s *SQLPipelineStrategy) RequiredSymbols() []string {
	if s.pipelineDir == "" {
		return nil
	}
	files, err := readPipelineDir(s.pipelineDir)
	if err != nil {
		return nil
	}

	symMap := make(map[string]bool)
	symRegex := regexp.MustCompile(`(?i)\bsymbol\s*=\s*'([A-Za-z0-9]+)'`)

	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".sql") {
			content, err := readPipelineFile(s.pipelineDir, f.Name())
			if err != nil {
				continue
			}
			matches := symRegex.FindAllStringSubmatch(string(content), -1)
			for _, m := range matches {
				if len(m) > 1 {
					sym := strings.ToUpper(strings.TrimSpace(m[1]))
					if sym != "" {
						symMap[sym] = true
					}
				}
			}
		}
	}

	var syms []string
	for sym := range symMap {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	return syms
}

// openAttachedCalcDB opens calcDBPath, attaches marketDBPath read-only as
// "market", and creates the temp view every pipeline script reads from.
// Shared by SQLPipelineStrategy.GenerateSignals and any other pipeline runner
// (e.g. decisiontree.go's SQL-backed feature extraction) that needs the same
// market-DB-as-source-of-truth setup without duplicating the DSN/attach
// boilerplate.
func openAttachedCalcDB(marketDBPath, calcDBPath string) (*sqlx.DB, error) {
	// The calc DB's directory may not exist yet (livescan writes to a fresh temp
	// directory on App Engine). Without it the ATTACH below fails with "unable to
	// open database file", the pipeline returns no signals, and a real entry signal
	// is reported as NO_SIGNAL.
	if dir := filepath.Dir(calcDBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create calc DB dir %s: %w", dir, err)
		}
	}

	dsn := calcDBPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_busy_timeout=15000&_journal_mode=WAL"
	} else {
		dsn += "&_busy_timeout=15000"
	}

	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open calc DB %s: %w", calcDBPath, err)
	}

	attachQuery := fmt.Sprintf("ATTACH DATABASE '%s' AS market;", marketDBPath)
	if _, err := db.Exec(attachQuery); err != nil {
		db.Close()
		return nil, fmt.Errorf("attach market database %s: %w", marketDBPath, err)
	}

	if _, err := db.Exec("CREATE TEMP VIEW IF NOT EXISTS backtest_start AS SELECT rowid, * FROM market.backtest_start;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("create backtest_start view: %w", err)
	}

	return db, nil
}

// GenerateSignals executes the SQL pipeline scripts in order and extracts entry signals.
func (s *SQLPipelineStrategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	if s.marketDBPath == "" || s.calcDBPath == "" {
		log.Printf("Warning: SQL strategy %s requires both marketDBPath and calcDBPath", s.id)
		return nil
	}

	db, err := openAttachedCalcDB(s.marketDBPath, s.calcDBPath)
	if err != nil {
		log.Printf("Warning: SQL strategy %s failed to open pipeline DB: %v", s.id, err)
		return nil
	}
	defer db.Close()

	// Execute pipeline scripts in lexical order
	files, err := readPipelineDir(s.pipelineDir)
	if err != nil {
		// Never fail silently: with no SQL run there are no slice tables, and a
		// real entry signal would be reported as NO_SIGNAL.
		log.Printf("[sql_strategy %s] ERROR: cannot read pipeline %s, no signals will be produced: %v", s.id, s.pipelineDir, err)
	} else {
		var sqlFiles []string
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".sql") {
				lower := strings.ToLower(f.Name())
				// Skip post-backtest reporting and audit queries that operate on equity_curve
				if strings.Contains(lower, "audit") || strings.Contains(lower, "annual") || strings.Contains(lower, "report") || strings.Contains(lower, "compare") {
					continue
				}
				sqlFiles = append(sqlFiles, f.Name())
			}
		}
		sort.Strings(sqlFiles)

		for _, sqlFile := range sqlFiles {
			content, err := readPipelineFile(s.pipelineDir, sqlFile)
			if err != nil {
				log.Printf("[sql_strategy %s] failed to read %s: %v", s.id, sqlFile, err)
				continue
			}
			sqlText := substitutePlaceholders(string(content), s.id, filepath.Base(sqlFile), s.config)
			queries := strings.Split(sqlText, ";")
			for _, q := range queries {
				trimmed := strings.TrimSpace(q)
				if trimmed == "" {
					continue
				}
				if _, err := db.Exec(trimmed); err != nil {
					log.Printf("[sql_strategy %s] SQL exec error in %s: %v (query: %.80s...)", s.id, filepath.Base(sqlFile), err, trimmed)
				}
			}
		}
	}

	// Extract generated entry signals from any available signal table
	var signals []models.Signal
	baseDir := filepath.Base(s.pipelineDir)
	cleanID := strings.TrimSuffix(strings.ReplaceAll(s.id, "-", "_"), "_sql")
	tableCandidates := []string{
		cleanID + "_signals",
		baseDir + "_signals",
		"rsi_oversold_signals",
		"entry",
		"signals",
	}

	for _, tbl := range tableCandidates {
		// First try extended select with optional direction, regime, and overrides
		extendedQuery := fmt.Sprintf(`
			SELECT coalesce(idx, rowid, 0) as idx, symbol, substr(date, 1, 10) as date,
			       open, high, low, close, volume, buylimit, entry,
			       coalesce(direction, 'LONG') as direction,
			       coalesce(regime, 'All Regimes') as regime,
			       coalesce(hold_days_override, 0) as hold_days_override,
			       coalesce(allocation_pct_override, 0.0) as allocation_pct_override,
			       coalesce(take_profit, 0.0) as take_profit,
			       coalesce(stop_loss, 0.0) as stop_loss
			FROM %s
			WHERE entry <> 0
			ORDER BY date, entry, symbol ASC;
		`, tbl)
		err = db.Select(&signals, extendedQuery)
		if err == nil && len(signals) > 0 {
			break
		}

		// Fallback to basic columns with safe coalesce on idx
		query := fmt.Sprintf(`
			SELECT coalesce(idx, rowid, 0) as idx, symbol, substr(date, 1, 10) as date,
			       open, high, low, close, volume, buylimit, entry,
			       0.0 as allocation_pct_override
			FROM %s
			WHERE entry <> 0
			ORDER BY date, entry, symbol ASC;
		`, tbl)
		err = db.Select(&signals, query)
		if err == nil && len(signals) > 0 {
			break
		}
	}

	if len(signals) == 0 {
		// Fallback query if standard signal tables aren't found
		fallbackQuery := `
			SELECT b.idx, b.symbol, substr(b.date, 1, 10) as date, b.open, b.high, b.low, b.close, b.volume, b.close as buylimit, 1 as entry
			FROM backtest_start b
			INNER JOIN entry e ON b.symbol = e.symbol AND substr(b.date, 1, 10) = substr(e.date, 1, 10)
			ORDER BY b.date, b.symbol ASC;
		`
		_ = db.Select(&signals, fallbackQuery)
	}

	return signals
}

// hasGoStrategy reports whether a strategy defined in package strategy (its
// init-registered Go types, not another SQL pipeline) corresponds to a
// sql/strategies folder name, ignoring case, dashes and underscores.
func hasGoStrategy(dirName string) bool {
	registryLock.RLock()
	defer registryLock.RUnlock()
	want := normalizeKey(dirName)
	for _, s := range registry {
		if _, isSQL := s.(*SQLPipelineStrategy); isSQL {
			continue
		}
		if normalizeKey(s.ID()) == want {
			return true
		}
	}
	return false
}

var (
	autoRegisteredMu sync.Mutex
	autoRegistered   = map[string]bool{}
)

// AutoRegisterSQLStrategies registers the pipeline behind each strategy
// defined in pkg/strategy. A sql/strategies folder with no matching Go
// strategy in this package (abandoned)
// is NOT registered: strategies come only from pkg/strategy, never
// from other folders or subfolders.
//
// It is idempotent: repeated calls with the same rootDir and default DB path
// (e.g. one per live scan in a long-running process) register once and then
// return immediately. Strategies added on disk after the first call are not
// picked up.
func AutoRegisterSQLStrategies(rootDir string, defaultDBPath ...string) {
	key := rootDir + "\x00" + strings.Join(defaultDBPath, "\x00")
	autoRegisteredMu.Lock()
	defer autoRegisteredMu.Unlock()
	if autoRegistered[key] {
		return
	}
	autoRegistered[key] = true

	stratDir := filepath.Join(rootDir, "sql", "strategies")
	entries, err := os.ReadDir(stratDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			dirName := entry.Name()
			if !hasGoStrategy(dirName) {
				continue
			}
			id := fmt.Sprintf("%s-sql", dirName)
			name := fmt.Sprintf("%s (sql/strategies)", strings.Title(strings.ReplaceAll(dirName, "_", " ")))
			desc := fmt.Sprintf("SQL pipeline executed from sql/strategies/%s", dirName)
			pipelinePath := filepath.Join(stratDir, dirName)

			cfg := StrategyConfig{
				ID:                 id,
				Name:               name,
				Description:        desc,
				TargetPct:          1.20,
				StopLossPct:        0.93,
				HoldingWindow:      10,
				PositionCap:        5,
				AllocationPct:      0.20,
				SlippagePct:        0.0005,
				CommissionPerShare: 0.0001,
			}

			NewSQLPipelineStrategy(id, name, desc, pipelinePath, cfg)
		}
	}
}

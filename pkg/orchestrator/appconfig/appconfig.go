// Package appconfig resolves every piece of environment-driven configuration
// the orchestrator needs — the schwaber client config, database and market-data
// paths, and the strategy allowlist — into one struct, constructed once in
// cmd/orchestrator and threaded down as explicit function arguments. Nothing
// below cmd/orchestrator should call os.Getenv for orchestrator config
// directly.
package appconfig

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
)

const (
	// EnvFolder is the APP_FOLDER env var: scratch and reference files for backtestgosqlite
	// (market bars, strategies.db, model databases). The operational database is Turso.
	EnvFolder = "APP_FOLDER"

	// TestDBName is the SQLite file name Load picks under `go test` only.
	TestDBName = "orchestrator.db"
)

// Config holds every resolved dependency/config value the orchestrator's job
// packages need. It is built once per invocation via Load and passed down
// explicitly.
type Config struct {
	Schwab *config.Config

	// DBPath is empty in production: the operational database is Turso (TURSO_DATABASE_URL).
	// Under `go test` Load fills it with a SQLite file (ORCHESTRATOR_DB, else under APP_FOLDER).
	DBPath   string
	MarketDB string

	StrategyAllowlist []string

	// Risk are the pre-trade limits (pkg/risk): RISK_DRAWDOWN_LADDER ("0.10:0.5,0.20:0"),
	// RISK_DAILY_LOSS_PCT (0.05) and RISK_MAX_GROSS_PCT (1.0). Unset means the defaults;
	// set to 0 (or an empty ladder) to turn one limit off.
	Risk risk.Limits
}

// Load resolves schwaber's client config (folder/token path) plus the local
// job-package env vars into one Config, without process exit — safe for HTTP
// handlers and tests.
func Load() (*Config, error) {
	schwabCfg := config.Load()
	folder, err := applySchwabPaths(schwabCfg)
	if err != nil {
		return nil, err
	}

	// GCS token sync (pull-if-newer / push-after-save) lives entirely inside
	// schwaber's schwab.Client now (pkg/tokensync, called from
	// ensureFreshToken/saveToken) — nothing to do here.

	dbPath := ""
	if testing.Testing() {
		dbPath = filepath.Join(folder, TestDBName)
		if v := strings.TrimSpace(os.Getenv("ORCHESTRATOR_DB")); v != "" {
			dbPath = v
		}
	}

	marketDB := strings.TrimSpace(os.Getenv("MARKET_DB"))

	var allowlist []string
	if raw := strings.TrimSpace(os.Getenv("STRATEGY_ALLOWLIST")); raw != "" {
		for part := range strings.SplitSeq(raw, ",") {
			id := strings.TrimSpace(part)
			if id != "" {
				allowlist = append(allowlist, id)
			}
		}
	}

	limits, err := riskLimits()
	if err != nil {
		return nil, err
	}

	return &Config{
		Schwab:            schwabCfg,
		DBPath:            dbPath,
		MarketDB:          marketDB,
		StrategyAllowlist: allowlist,
		Risk:              limits,
	}, nil
}

// riskLimits reads the RISK_* variables over risk.DefaultLimits.
func riskLimits() (risk.Limits, error) {
	l := risk.DefaultLimits()
	if raw, ok := os.LookupEnv("RISK_DRAWDOWN_LADDER"); ok {
		ladder, err := risk.ParseLadder(raw)
		if err != nil {
			return l, fmt.Errorf("RISK_DRAWDOWN_LADDER: %w", err)
		}
		l.Ladder = ladder
	}
	for name, dst := range map[string]*float64{"RISK_DAILY_LOSS_PCT": &l.DailyLossPct, "RISK_MAX_GROSS_PCT": &l.MaxGrossPct, "RISK_MAX_ORDER_PCT": &l.MaxOrderPct, "RISK_AUTOKILL_DRAWDOWN": &l.AutoKillDrawdown} {
		raw, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || v < 0 {
			return l, fmt.Errorf("%s=%q: want a fraction such as 0.05 (0 turns the limit off)", name, raw)
		}
		*dst = v
	}
	for name, dst := range map[string]*int{"RISK_MAX_ORDERS_PER_DAY": &l.MaxOrdersPerDay, "RISK_MAX_POSITIONS": &l.MaxPositions, "RISK_AUTOKILL_FAILURES": &l.AutoKillFailures} {
		raw, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || v < 0 {
			return l, fmt.Errorf("%s=%q: want a whole number (0 turns the limit off)", name, raw)
		}
		*dst = v
	}
	return l, nil
}

// MustLoad is Load, but exits the process on error — for CLI entrypoints
// that can't proceed without valid config.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		log.Fatalf("%v", err)
	}
	return cfg
}

// applySchwabPaths resolves APP_FOLDER (creating it, falling back to the
// working directory if unwritable) and points cfg.TokenPath at
// APP_FOLDER/token.json when it isn't already absolute. Returns the
// resolved folder.
func applySchwabPaths(cfg *config.Config) (string, error) {
	folder := strings.TrimSpace(os.Getenv(EnvFolder))
	if folder == "" {
		return "", fmt.Errorf("%s must be set", EnvFolder)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("%s=%s cannot be created (%w): set %s to a writable folder (deploy_gae/config.sh set %s=<folder>; on App Engine only /tmp is writable)", EnvFolder, folder, err, EnvFolder, EnvFolder)
	}

	token := strings.TrimSpace(cfg.TokenPath)
	if token == "" {
		token = "token.json"
	}
	if !filepath.IsAbs(token) {
		token = filepath.Join(folder, filepath.Base(token))
		cfg.TokenPath = token
		if err := os.Setenv("SCHWAB_TOKEN_PATH", token); err != nil {
			return "", err
		}
	}
	return folder, nil
}

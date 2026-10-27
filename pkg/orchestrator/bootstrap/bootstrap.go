// Package bootstrap opens the orchestrator database (Turso) and ensures every package's schema.
// Every EnsureSchema is idempotent, so the CLI (cmd/orchestrator) and the HTTP
// surface (pkg/appserve) call the same function and always see the same schema
// regardless of which job ran first.
package bootstrap

import (
	"database/sql"
	"log"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/schemas"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// Load reads config, opens the database with every schema ensured, and logs
// the config in use. The caller closes env.DB.
func Load() (jobenv.Env, error) {
	cfg, err := appconfig.Load()
	if err != nil {
		return jobenv.Env{}, err
	}
	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		return jobenv.Env{}, err
	}
	if err := cfg.LogUsed(db); err != nil {
		log.Printf("[appconfig] log used config: %v", err)
	}
	return jobenv.Env{Cfg: cfg, DB: db}, nil
}

// OpenDB opens dbPath and ensures every package's schema exists.
func OpenDB(dbPath string) (*sql.DB, error) {
	db, err := appconfig.OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	if err := schemas.EnsureAll(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := dbutil.EnsureOnce("dashboard.EnsureSchema", func() error { return dashboard.EnsureSchema(db) }); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Package appserve is the shared HTTP surface for App Engine and `orchestrator serve`:
// dashboard, /cron/*, Schwab OAuth, and warmup/health. Same binary as the CLI.
package appserve

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtimeenv"
	"github.com/darianmavgo/trade_well/pkg/schwaber/oauthhttp"
)

// Serve starts the HTTP server (blocks). The environment was already filled from the GCS
// config object by main() (config.Bootstrap) before this is called.
func Serve(ctx context.Context, port int) error {
	info := runtimeenv.Detect()
	log.Printf("[appserve] starting %s", info)

	cfg, err := appconfig.Load()
	if err != nil {
		return fmt.Errorf("appconfig: %w", err)
	}

	if port <= 0 {
		port = 8080
		if p := os.Getenv("PORT"); p != "" {
			if _, err := fmt.Sscanf(p, "%d", &port); err != nil || port <= 0 {
				port = 8080
			}
		}
	}

	srv, err := dashboard.NewServer(cfg.DBPath, port)
	if err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}

	root := http.NewServeMux()
	root.HandleFunc("/_ah/warmup", writeOK)
	root.HandleFunc("/_ah/health", writeOK)
	if err := registerDiagnostics(root); err != nil {
		return err
	}
	if err := registerE2E(root); err != nil {
		return err
	}
	if err := registerKillswitch(root); err != nil {
		return err
	}
	if err := registerExitMarket(root); err != nil {
		return err
	}
	if err := registerClearStale(root); err != nil {
		return err
	}
	if err := registerCancelStaleEntries(root); err != nil {
		return err
	}
	if err := registerManageExitsPage(root); err != nil {
		return err
	}
	if err := registerCheckHealthRun(root); err != nil {
		return err
	}
	if err := registerEnv(root); err != nil {
		return err
	}
	for _, register := range []func(*http.ServeMux) error{registerOps, registerReadyPage, registerScanPage, registerConfigPage, registerAllowlistPage} {
		if err := register(root); err != nil {
			return err
		}
	}
	registerCron(root)
	oauthhttp.Register(root)

	dash, err := srv.Handler()
	if err != nil {
		return fmt.Errorf("dashboard handler: %w", err)
	}
	root.Handle("/", dash)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[appserve] listening on %s", addr)
	if err := logStartup(cfg.DBPath, "server_start", addr); err != nil {
		log.Printf("[appserve] failed to log startup: %v", err)
	}
	// App Engine stops an idle instance with SIGTERM and about 2 seconds of grace.
	// Return from Serve on it (instead of dying inside ListenAndServe) so
	// in-flight requests finish.
	sigCtx, stopSignals := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stopSignals()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serveUntilDone(sigCtx, &http.Server{Handler: withChrome(root)}, ln, time.Second)
}

// serveUntilDone serves srv on ln until ctx is done, then stops accepting
// requests and returns nil once srv has shut down (waiting up to grace for
// in-flight requests). Any other server error is returned.
func serveUntilDone(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Printf("[appserve] shutting down: no new requests; in-flight requests finish")
	shutCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
	}
	if err := <-errc; err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func writeOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// openAndEnsureSchemas opens dbPath and ensures every package's schema exists.
// It is the same bootstrap the CLI uses (pkg/bootstrap).
func openAndEnsureSchemas(dbPath string) (*sql.DB, error) {
	return bootstrap.OpenDB(dbPath)
}

var appserveSchemaDDL = orchestratorsql.Q("appserve/appserve_schema_ddl")

func logStartup(dbPath, event, detail string) error {
	db, err := appconfig.OpenDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(appserveSchemaDDL); err != nil {
		return err
	}
	_, err = db.Exec(orchestratorsql.Q("appserve/log_startup"), event, detail, time.Now().UTC())
	return err
}

// registerDiagnostics mounts /runtime. It shows the project, service and version, so it sits
// behind the dashboard's Basic Auth like everything else operator-only. Only /_ah/warmup and
// /_ah/health, which App Engine calls and which reveal nothing, stay open.
func registerDiagnostics(root *http.ServeMux) error {
	runtimeHandler, err := dashboard.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, runtimeenv.Detect().String())
	}))
	if err != nil {
		return fmt.Errorf("/runtime: %w", err)
	}
	root.Handle("/runtime", runtimeHandler)
	return nil
}

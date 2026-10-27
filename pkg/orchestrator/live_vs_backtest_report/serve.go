package live_vs_backtest_report

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
)

// ServiceName is the App Engine service. It is a separate deploy from the
// default trading service and reaches the same GCS bucket read-only.
const ServiceName = "live-vs-backtest-report"

const reportCacheFor = 2 * time.Minute

// Serve is the report service. It opens the orchestrator database (Turso) and serves the HTML
// report. It writes nothing to it; the per-run report database and HTML go to ScratchDir.
func Serve(ctx context.Context, port int) error {
	cfg, err := appconfig.Load()
	if err != nil {
		return err
	}
	var restoreErr error
	db, err := bootstrap.OpenDB(cfg.DBPath)
	if err != nil {
		restoreErr = err
		db = nil
	} else {
		db.SetMaxOpenConns(1)
		defer db.Close()
	}

	page, err := newHandler(cfg, db, restoreErr)
	if err != nil {
		return err
	}
	root := http.NewServeMux()
	root.HandleFunc("/_ah/warmup", writeOK)
	root.HandleFunc("/_ah/health", writeOK)
	root.Handle("/", page)

	if port <= 0 {
		port = 8080
		if p := os.Getenv("PORT"); p != "" {
			if _, err := fmt.Sscanf(p, "%d", &port); err != nil || port <= 0 {
				port = 8080
			}
		}
	}
	addr := fmt.Sprintf(":%d", port)
	log.Printf("[live_vs_backtest_report] listening on %s", addr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	srv := &http.Server{Handler: root}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-sigCtx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
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

type pageServer struct {
	mu         sync.Mutex
	cfg        *appconfig.Config
	db         *sql.DB
	restoreErr error
	html       []byte
	header     string
	built      time.Time
}

func newHandler(cfg *appconfig.Config, db *sql.DB, restoreErr error) (http.Handler, error) {
	page := &pageServer{cfg: cfg, db: db, restoreErr: restoreErr}
	return dashboard.RequireAuth(http.HandlerFunc(page.serve))
}

func (s *pageServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET", http.StatusMethodNotAllowed)
		return
	}
	if s.restoreErr != nil {
		writeFailed(w, s.restoreErr)
		return
	}
	body, header, err := s.report(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeFailed(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Reconcile-Report", header)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func writeFailed(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Reconcile-Report", "FAILED")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "live_vs_backtest_report FAILED\n%v\n", err)
}

func (s *pageServer) report(ctx context.Context, force bool) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.html != nil && time.Since(s.built) < reportCacheFor {
		return s.html, s.header, nil
	}
	if s.cfg == nil || s.db == nil {
		return nil, "", fmt.Errorf("live_vs_backtest_report: database is not open")
	}
	dir := ScratchDir(s.cfg)
	opts := Options{
		MarketDB: s.cfg.MarketDB,
		Table:    "backtest_start",

		HTML:      filepath.Join(dir, "live_vs_backtest_report.html"),
		Allowlist: s.cfg.StrategyAllowlist,
	}
	var logBuf io.Writer = io.Discard
	res, err := Run(ctx, logBuf, jobenv.Env{Cfg: s.cfg, DB: s.db}, opts)
	if err != nil {
		return nil, "", err
	}
	body, err := os.ReadFile(opts.HTML)
	if err != nil {
		return nil, "", err
	}
	s.html = body
	s.built = time.Now()
	s.header = "OK"
	if len(res.Warnings) > 0 {
		s.header = "OK, with warnings"
	}
	log.Printf("[live_vs_backtest_report] %s decisions=%d round_trips=%d warnings=%d", s.header, res.Decisions, res.RoundTrips, len(res.Warnings))
	return s.html, s.header, nil
}

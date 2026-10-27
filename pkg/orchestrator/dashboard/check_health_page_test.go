package dashboard

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

func checkHealthServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("DASHBOARD_USERNAME", "admin")
	t.Setenv("DASHBOARD_PASSWORD", "s3cr3t")
	srv, err := NewServer(filepath.Join(t.TempDir(), "orchestrator.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.db.Close() })
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return srv, h
}

func getCheckHealth(h http.Handler, method, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/check_health", nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// /check_health is behind the same Basic Auth as the rest of the dashboard.
func TestCheckHealthPageRequiresTheDashboardAuth(t *testing.T) {
	_, h := checkHealthServer(t)
	if w := getCheckHealth(h, "GET", "", ""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no credentials: got %d, want 401 with a WWW-Authenticate challenge (so Chrome prompts)", w.Code)
	}
	if w := getCheckHealth(h, "GET", "admin", "wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", w.Code)
	}
	if w := getCheckHealth(h, "GET", "admin", "s3cr3t"); w.Code != http.StatusOK {
		t.Errorf("correct credentials: got %d, want 200", w.Code)
	}
}

func TestCheckHealthPageServesHTMLAndNamesTheBrokenJob(t *testing.T) {
	srv, h := checkHealthServer(t)
	// A fresh database: nothing has run, so every due job is broken and the
	// first in pipeline order is the root cause.
	w := getCheckHealth(h, "GET", "admin", "s3cr3t")
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, Chrome needs text/html to render it", ct)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	for _, want := range []string{"<title>check_health</title>", "A scheduled job did not do its job", "account_state", "NEVER_RAN", "first job in the pipeline"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// Loading the page must not write anything.
	var n int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM check_health_finding`).Scan(&n); err != nil || n != 0 {
		t.Errorf("viewing the page wrote %d findings (err %v); it must be read-only", n, err)
	}
}

func TestCheckHealthPageShowsHealthyWhenEveryJobRan(t *testing.T) {
	srv, h := checkHealthServer(t)
	now := time.Now().UTC()
	for _, spec := range job_runs.Schedule {
		if _, err := job_runs.RecordJobRun(srv.db, job_runs.JobRun{
			Job: spec.Name, Status: job_runs.JobRunOK, Summary: "OK " + spec.Name, StartedAt: now, FinishedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	body := getCheckHealth(h, "GET", "admin", "s3cr3t").Body.String()
	if !strings.Contains(body, "Every scheduled job that is due recorded a run") || strings.Contains(body, "did not do its job") {
		t.Errorf("want the healthy banner, got:\n%s", body)
	}
}

func TestCheckHealthPageShowsStoredAuditAndFindingsEscaped(t *testing.T) {
	srv, h := checkHealthServer(t)
	now := time.Now().UTC()
	if err := check_health.RecordAudit(srv.db, &check_health.Audit{
		AccountNumber: "25474194", Timestamp: now, TotalPositions: 2, ProtectedPositions: 1,
		UnprotectedPositions: 1, Status: "ACTION_REQUIRED", CriticalFindings: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := check_health.RecordFinding(srv.db, &check_health.Finding{
		RunAt: now, AccountNumber: "25474194", CheckName: "missing_stop_loss", Category: "position_protection",
		Severity: "CRITICAL", Subject: "MARA", Detail: `no stop <script>alert(1)</script>`,
	}); err != nil {
		t.Fatal(err)
	}
	body := getCheckHealth(h, "GET", "admin", "s3cr3t").Body.String()
	for _, want := range []string{"25474194", "ACTION_REQUIRED", "missing_stop_loss", "MARA"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("finding text must be HTML-escaped")
	}
}

func TestCheckHealthPageIsGETOnly(t *testing.T) {
	_, h := checkHealthServer(t)
	if w := getCheckHealth(h, "POST", "admin", "s3cr3t"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", w.Code)
	}
}

func TestCheckHealthPageShowsCurrentPositionsFirst(t *testing.T) {
	v := newCheckHealthView(&check_health.Status{
		At: time.Now(),
		Positions: []check_health.PositionAudit{{AccountNumber: "1", Symbol: "MARA", Quantity: 5, Status: "CRITICAL",
			Issues: []string{"no exit date <b>"}}},
	})
	var sb strings.Builder
	if err := checkHealthTmpl.Execute(&sb, v); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "Current positions") || !strings.Contains(out, "MARA") || !strings.Contains(out, "unknown") {
		t.Errorf("positions table missing:\n%s", out)
	}
	if strings.Index(out, "Current positions") > strings.Index(out, "Scheduled jobs") {
		t.Error("current positions must come before the scheduled jobs")
	}
	if strings.Contains(out, "<b>") {
		t.Error("issue text must be escaped")
	}
}

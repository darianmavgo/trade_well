package appserve

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every job has a /cron/<job> route that refuses requests without App
// Engine's cron header. The job names match the pkg/ directory names.
func TestCronRoutesExistAndRequireCronHeader(t *testing.T) {
	mux := http.NewServeMux()
	registerCron(mux)
	for _, job := range []string{"account_state", "scan_market", "stage_orders", "submit_orders", "manage_exits", "check_health"} {
		req := httptest.NewRequest("GET", "/cron/"+job, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("/cron/%s without cron header = %d, want 403", job, w.Code)
		}
	}
}

// noEmail empties the SMTP settings so the failure email takes the real "configuration is
// incomplete" path and nothing is sent from a test, whatever the developer's environment holds.
func noEmail(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SMTP_HOST", "SMTP_USER", "SMTP_PASS", "EMAIL_RECIPIENT"} {
		t.Setenv(k, "")
	}
}

func TestFailureSubjectNamesTheJob(t *testing.T) {
	if got := failureSubject("scan_market"); got != "scan_market FAILED" {
		t.Fatalf("subject = %q", got)
	}
}

func TestWriteJobErrReturns500(t *testing.T) {
	noEmail(t)
	w := httptest.NewRecorder()
	writeJobErr(w, "scan_market", errors.New("STALE_MARKET_DATA: tip 2026-09-22"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "scan_market: STALE_MARKET_DATA") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestCronPanicIsReportedNotSwallowed(t *testing.T) {
	noEmail(t)
	h := cronOnly(func(http.ResponseWriter, *http.Request) { panic("boom") })
	req := httptest.NewRequest("GET", "/cron/manage_exits", nil)
	req.Header.Set("X-Appengine-Cron", "true")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "manage_exits") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestFailureEmailBodyIsTheJobReportTemplate(t *testing.T) {
	body := failureEmailBody("scan_market", errors.New("STALE_MARKET_DATA: <tip> 2026-09-22"), time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC))
	for _, want := range []string{"<html>", "<table", "scan_market", "FAILED", "color:#c62828", "2026-10-15T13:00:00Z", "&lt;tip&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("failure email lacks %q:\n%s", want, body)
		}
	}
}

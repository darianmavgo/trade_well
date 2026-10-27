package appenv

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReportFileNeverDoublesPrefix(t *testing.T) {
	t.Setenv("APP_FOLDER", "/app")
	want := filepath.Join("/app", "data", "reports", "x.html")
	for _, in := range []string{"x.html", "reports/x.html", "data/reports/x.html"} {
		if got := ReportFile(in); got != want {
			t.Errorf("ReportFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// The .env file is read for local runs, the process environment beats it, and IgnoreDotEnv
// (what the orchestrator calls on App Engine) turns the file off even after it was read.
func TestDotEnvIsLocalOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APPENV_TEST_ONLY_IN_FILE=from-file\nAPPENV_TEST_BOTH=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("APPENV_TEST_BOTH", "env")
	once, fileKV = sync.Once{}, nil
	dotEnvOff.Store(false)
	t.Cleanup(func() { once, fileKV = sync.Once{}, nil; dotEnvOff.Store(false) })

	if got := Get("APPENV_TEST_ONLY_IN_FILE"); got != "from-file" {
		t.Fatalf("local run should read .env, got %q", got)
	}
	if got := Get("APPENV_TEST_BOTH"); got != "env" {
		t.Fatalf("environment should beat .env, got %q", got)
	}

	IgnoreDotEnv()
	if got := Get("APPENV_TEST_ONLY_IN_FILE"); got != "" {
		t.Fatalf("after IgnoreDotEnv the file must not be used, got %q", got)
	}
	if got := Get("APPENV_TEST_BOTH"); got != "env" {
		t.Fatalf("the environment must still work, got %q", got)
	}
}

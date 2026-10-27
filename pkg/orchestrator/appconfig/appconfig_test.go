package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadResolvesDBPath: the operational database is Turso, so DBPath is only a test file.
// Under `go test` Load derives it from APP_FOLDER (TestDBName) unless ORCHESTRATOR_DB overrides it.
func TestLoadResolvesDBPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvFolder, dir)
	t.Setenv("ORCHESTRATOR_DB", "")
	t.Setenv("SCHWAB_APP_KEY", "test-key")
	t.Setenv("SCHWAB_APP_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantDB := filepath.Join(dir, TestDBName)
	if cfg.DBPath != wantDB {
		t.Fatalf("DBPath=%q want %q", cfg.DBPath, wantDB)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("expected APP_FOLDER to exist: %v", err)
	}
}

// TestLoadHonorsOrchestratorDBOverride covers the new ORCHESTRATOR_DB escape
// hatch, which internal/appdata had no equivalent of.
func TestLoadHonorsOrchestratorDBOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "custom.db")
	t.Setenv(EnvFolder, dir)
	t.Setenv("ORCHESTRATOR_DB", override)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != override {
		t.Fatalf("DBPath=%q want %q", cfg.DBPath, override)
	}
}

func TestLoadRequiresAppFolder(t *testing.T) {
	t.Setenv(EnvFolder, "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when APP_FOLDER is empty")
	}
}

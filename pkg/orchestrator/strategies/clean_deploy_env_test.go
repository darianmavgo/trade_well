package strategies

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cleanScript = "../../../deploy_gae/clean_deploy_env.sh"

func runCleanScript(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command("bash", append([]string{cleanScript}, args...)...).CombinedOutput()
	return string(out), err
}

// --scan-only reads the real codebase: variables the code reads show up, and nothing is uploaded.
func TestCleanDeployEnvScanFindsWhatTheCodeReads(t *testing.T) {
	out, err := runCleanScript(t, "--scan-only")
	if err != nil {
		t.Fatalf("scan failed: %v\n%s", err, out)
	}
	for _, want := range []string{"STRATEGY_ALLOWLIST", "SCHWAB_APP_KEY", "TURSO_DATABASE_URL", "TZ", "SCHWAB_ACCOUNT_NUMBERS"} {
		if !strings.Contains("\n"+out, "\n"+want+"\n") {
			t.Errorf("scan did not find %s", want)
		}
	}
}

// Offline run on a real file: unused variables go, used ones stay with their values, a duplicate
// keeps its last value, a key the scan cannot judge stays, and no value reaches the report.
func TestCleanDeployEnvKeepsWhatIsNeeded(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.env")
	outFile := filepath.Join(dir, "out.env")
	body := "# comment\nSTRATEGY_ALLOWLIST=a+b,c\nTZ=America/New_York\nSCHWAB_APP_SECRET=secret-value-1\n" +
		"SCHWAB_CLIENT_SECRET=old-secret-value\nZZ_NOT_USED_ANYWHERE_12345=gone-value\n\nDRY_RUN_DEFAULT=true\nDRY_RUN_DEFAULT=false\nlower_case_key=odd-value\n"
	if err := os.WriteFile(in, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runCleanScript(t, "--from-file", in, "--out", outFile)
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, report)
	}
	for _, leak := range []string{"secret-value-1", "old-secret-value", "gone-value", "odd-value", "a+b,c"} {
		if strings.Contains(report, leak) {
			t.Errorf("the report printed a value (%q); only names may be shown", leak)
		}
	}
	gotBytes, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	got := "\n" + string(gotBytes)
	for _, want := range []string{"\nSTRATEGY_ALLOWLIST=a+b,c", "\nTZ=America/New_York", "\nSCHWAB_APP_SECRET=secret-value-1", "\nDRY_RUN_DEFAULT=false\n", "\nlower_case_key=odd-value"} {
		if !strings.Contains(got, want) {
			t.Errorf("cleaned env lost %q:\n%s", strings.TrimSpace(want), gotBytes)
		}
	}
	for _, gone := range []string{"SCHWAB_CLIENT_SECRET", "ZZ_NOT_USED_ANYWHERE_12345", "# comment", "DRY_RUN_DEFAULT=true"} {
		if strings.Contains(got, gone) {
			t.Errorf("cleaned env still has %q:\n%s", gone, gotBytes)
		}
	}
	if st, err := os.Stat(outFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("output file mode = %v (%v), want 0600", st.Mode().Perm(), err)
	}
}

// A file with nothing to remove is reported clean and no output file is written.
func TestCleanDeployEnvAlreadyClean(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.env")
	if err := os.WriteFile(in, []byte("STRATEGY_ALLOWLIST=a\nTZ=America/New_York\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(dir, "out.env")
	report, err := runCleanScript(t, "--from-file", in, "--out", outFile)
	if err != nil || !strings.Contains(report, "already clean") {
		t.Fatalf("want already clean, got err=%v\n%s", err, report)
	}
	if _, err := os.Stat(outFile); err == nil {
		t.Error("an already-clean run must not write an output file")
	}
}

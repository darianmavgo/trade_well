package strategies

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var yamlEnvLine = regexp.MustCompile(`^\s+([A-Za-z_][A-Za-z0-9_]*):\s*"?(.*?)"?\s*(#.*)?$`)

// appYAMLEnv returns the env_variables block of an app.yaml as key -> value.
func appYAMLEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "env_variables:"):
			in = true
		case in && len(line) > 0 && line[0] != ' ' && line[0] != '#':
			in = false
		case in:
			if m := yamlEnvLine.FindStringSubmatch(line); m != nil && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				env[m[1]] = strings.TrimSpace(m[2])
			}
		}
	}
	return env
}

// Configuration, STRATEGY_ALLOWLIST included, lives in gs://<bucket>/config/env.
// An env_variables block in an app.yaml would silently override that object (an
// environment variable beats it), so these files stay free of one.
func TestAppYAMLHasNoEnv(t *testing.T) {
	for _, path := range []string{
		"../../../deploy_gae/app.yaml",
		"../../../deploy_gae/live_vs_backtest_report.yaml",
		"../../../deploy_live_local/app.yaml",
		"../../../deploy_report_local/app.yaml",
	} {
		if env := appYAMLEnv(t, path); len(env) > 0 {
			t.Errorf("%s has env_variables %v: put them in gs://<bucket>/config/env (deploy_gae/config.sh)", path, env)
		}
	}
}

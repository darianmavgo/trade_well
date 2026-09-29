package config

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	schwaberconfig "github.com/darianmavgo/schwaber/pkg/config"
)

// LoadDotEnv sets each KEY=VALUE of the first readable file in paths that is not
// already set in the environment (an exported variable always wins). It returns the
// file used ("" when none was found) and the variable names it set, never values.
// ibkr_api keeps a local .env next to it for the account id, username and trading
// mode; the rest of the system's configuration is the shared GCS object.
func LoadDotEnv(paths ...string) (used string, set []string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			if k != "" && os.Getenv(k) == "" {
				os.Setenv(k, v)
				set = append(set, k)
			}
		}
		return p, set
	}
	return "", nil
}

// Bootstrap fills the environment for an ibkr_api binary: first the local .env
// (IBKR_ENV_FILE, else ./.env, else the .env next to this module when run from a
// sibling directory), then the shared GCS config object through schwaber's
// Bootstrap. The .env comes first so its values win; both never overwrite a variable
// that is already exported. The GCS step is best effort here (a warning, not a
// failure): an IBKR-only session needs nothing from it.
func Bootstrap(ctx context.Context) (envFile string, err error) {
	candidates := []string{os.Getenv("IBKR_ENV_FILE"), ".env"}
	if exe, e := os.Executable(); e == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", ".env"))
	}
	candidates = append(candidates, "../ibkr_api/.env")
	var nonEmpty []string
	for _, c := range candidates {
		if c != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}
	envFile, _ = LoadDotEnv(nonEmpty...)
	if _, gerr := schwaberconfig.Bootstrap(ctx); gerr != nil {
		log.Printf("[ibkr] shared config object not loaded (continuing with the .env and the environment): %v", gerr)
	}
	return envFile, nil
}

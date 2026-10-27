package strategies

import (
	"os"
	"path"
	"strings"
	"testing"
)

// A local .env may sit in the project root, but the App Engine upload must never include it and
// the deployed app must not depend on it: its configuration is gs://<bucket>/config/env. The
// upload is controlled by .gcloudignore, which ignores everything and re-includes only source
// directories. This fails if that stops being true: ignore-all is no longer first, or a later
// "!" rule could bring back .env or a similar secrets file at the project root.
func TestGAEUploadNeverIncludesDotEnv(t *testing.T) {
	raw, err := os.ReadFile("../../../.gcloudignore")
	if err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			rules = append(rules, line)
		}
	}
	if len(rules) == 0 || rules[0] != "*" {
		t.Fatalf(".gcloudignore must start by ignoring everything (\"*\"), first rule is %q", rules)
	}
	secrets := []string{".env", ".env.local", ".env.production", "token.json", "credentials.json", "schwaber.db"}
	for _, rule := range rules[1:] {
		pat, negated := strings.CutPrefix(rule, "!")
		if !negated {
			continue
		}
		pat = strings.TrimPrefix(pat, "/")
		for _, name := range secrets {
			if ok, _ := path.Match(pat, name); ok {
				t.Errorf(".gcloudignore rule %q would upload %s", rule, name)
			}
		}
	}
}

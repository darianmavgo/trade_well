// Package configview shows the app's configuration object (gs://<bucket>/config/env) for the
// /config page: every variable name, the value only where it is not a secret, and whether
// STRATEGY_ALLOWLIST resolves from the strategy bundle compiled into this binary.
package configview

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
)

// secretKey matches names whose value must never be shown: passwords, tokens, keys, secrets,
// client ids, logins and account numbers.
var secretKey = regexp.MustCompile(`(?i)PASS|PWD|SECRET|TOKEN|KEY|CREDENTIAL|AUTH|CLIENT_ID|USERNAME|ACCOUNT|EMAIL`)

// Masked is shown in place of a secret value.
const Masked = "••••••••"

// Var is one variable of the object.
type Var struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// Entry is one STRATEGY_ALLOWLIST entry (a strategy id or an a+b+c stack) and whether it resolves.
type Entry struct {
	Entry   string   `json:"entry"`
	Members []string `json:"members"`
	OK      bool     `json:"ok"`
	Problem string   `json:"problem,omitempty"`
}

// View is the page.
type View struct {
	Object    string                      `json:"object"`
	Vars      []Var                       `json:"vars"`
	Allowlist string                      `json:"allowlist"`
	Entries   []Entry                     `json:"entries"`
	Bundle    strategies.BundledAssetInfo `json:"bundle"`
	Candidate string                      `json:"candidate,omitempty"` // an allowlist the operator is trying
	Trial     []Entry                     `json:"trial,omitempty"`
}

// Vars lists the variables of an env-format object, sorted, with secrets masked.
func Vars(data []byte) []Var {
	_, values := config.ParseEnv(data)
	out := make([]Var, 0, len(values))
	for k, v := range values {
		e := Var{Name: k, Value: v}
		if secretKey.MatchString(k) {
			e.Value, e.Secret = Masked, true
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Check resolves every entry of a comma-separated allowlist against the bundle and says which
// members fail. It changes nothing.
func Check(list string) []Entry {
	ids, err := strategies.Parse(list)
	if err != nil {
		return []Entry{{Entry: list, Problem: err.Error()}}
	}
	bundle, berr := strategies.ReadBundledStrategyAssetInfo()
	var out []Entry
	for _, entry := range ids {
		out = append(out, checkEntry(entry, bundle, berr))
	}
	return out
}

// checkEntry resolves one entry (an id or an a+b+c stack) against an already-read bundle.
func checkEntry(entry string, bundle strategies.BundledAssetInfo, berr error) Entry {
	e := Entry{Entry: entry, Members: []string{entry}, OK: true}
	if strategy.IsStack(entry) {
		e.Members = strategy.ParseStack(entry)
	}
	var bad []string
	for _, id := range e.Members {
		got, err := strategies.ResolveGreenlit(nil, []string{id})
		if err != nil || len(got) == 0 {
			bad = append(bad, id+" is not registered")
			continue
		}
		if _, row := got[0].(interface{ Family() string }); row && berr == nil && !slices.Contains(bundle.IDs, id) {
			bad = append(bad, id+" is a row missing from the bundle")
		}
	}
	if len(bad) > 0 {
		e.OK, e.Problem = false, strings.Join(bad, "; ")
	}
	return e
}

// Load reads the config object from GCS and builds the view. candidate, when not empty, is an
// allowlist to try before saving it.
func Load(ctx context.Context, candidate string) (View, error) {
	bucket := config.Bucket()
	v := View{Object: fmt.Sprintf("gs://%s/%s", bucket, config.ConfigObject)}
	data, err := tokensync.GetObject(ctx, bucket, config.ConfigObject)
	if err != nil {
		return v, fmt.Errorf("read %s: %w", v.Object, err)
	}
	v.Vars = Vars(data)
	_, values := config.ParseEnv(data)
	v.Allowlist = values[strategies.EnvAllowlist]
	if v.Allowlist != "" {
		v.Entries = Check(v.Allowlist)
	}
	if b, err := strategies.ReadBundledStrategyAssetInfo(); err == nil {
		v.Bundle = b
	}
	if strings.TrimSpace(candidate) != "" {
		v.Candidate = candidate
		v.Trial = Check(candidate)
	}
	return v, nil
}

// Choice is one line of the allowlist picker: a registered strategy id, or an entry already in
// the list (a stack, for instance) that has to stay pickable so it can be kept or dropped.
type Choice struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Selected bool   `json:"selected"`
	OK       bool   `json:"ok"`                // resolves from this binary's bundle
	Problem  string `json:"problem,omitempty"` // why not, when it does not
}

// Choices lists every strategy this binary can resolve, with the ones in current selected,
// plus any entry of current that is not a single registered id (a+b+c stacks). Sorted by id.
func Choices(current []string) []Choice {
	_ = strategies.EnsureBundle()
	in := make(map[string]bool, len(current))
	for _, id := range current {
		in[id] = true
	}
	bundle, berr := strategies.ReadBundledStrategyAssetInfo()
	var out []Choice
	seen := map[string]bool{}
	for _, s := range strategy.List() {
		id := s.ID()
		seen[id] = true
		out = append(out, choice(id, s.Name(), in[id], bundle, berr))
	}
	for _, id := range current {
		if !seen[id] {
			out = append(out, choice(id, "", true, bundle, berr))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func choice(id, name string, selected bool, bundle strategies.BundledAssetInfo, berr error) Choice {
	c := Choice{ID: id, Name: name, Selected: selected, OK: true}
	if e := checkEntry(id, bundle, berr); !e.OK {
		c.OK, c.Problem = false, e.Problem
	}
	return c
}

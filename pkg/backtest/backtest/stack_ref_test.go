package backtest

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
)

func TestResolveStackRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refdb.AddStack(db, "Win Rate Ten", []string{"first-one", "second-two", "third"}, ""); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for arg, want := range map[string]string{
		"stack-001":         "first-one+second-two+third",
		"Win Rate Ten":      "first-one+second-two+third",
		"win-rate_ten":      "first-one+second-two+third",
		"a+b":               "a+b",               // an inline stack passes through
		"a,b":               "a,b",               // so does a comma list
		"not-a-saved-stack": "not-a-saved-stack", // and so does anything unknown
		"":                  "",
	} {
		got, err := resolveStackRef(arg, path)
		if err != nil || got != want {
			t.Errorf("resolveStackRef(%q) = %q, %v; want %q", arg, got, err, want)
		}
	}
	// No strategies database: nothing to look up, nothing created.
	missing := filepath.Join(t.TempDir(), "none.db")
	if got, err := resolveStackRef("stack-001", missing); err != nil || got != "stack-001" {
		t.Errorf("without a database: %q, %v", got, err)
	}
}

func TestSaveStackFromPick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	conf := DefaultConfig()
	conf.StrategiesDb = path
	conf.SaveStack = "Picked Two"
	res := runner.PickResult{Picks: []runner.Pick{{ID: "one"}, {ID: "two"}}}
	if err := saveStack(conf, res); err != nil {
		t.Fatal(err)
	}
	got, err := resolveStackRef("Picked Two", path)
	if err != nil || got != "one+two" {
		t.Fatalf("saved stack resolves to %q, %v", got, err)
	}
	if err := saveStack(conf, res); err == nil {
		t.Fatal("the same label was saved twice")
	}
	if err := saveStack(conf, runner.PickResult{}); err == nil {
		t.Fatal("an empty pick was saved")
	}
}

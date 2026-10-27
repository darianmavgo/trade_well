package deploy_strategy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

func names(steps []Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Name)
	}
	return out
}

func TestPlanKeepsTheLiveListAndAddsTheNewRow(t *testing.T) {
	current := []string{"markov_model_mull+streak-shpu-down3", "hmm-calm-soxl"}
	bundled := []string{"markov_model_mull", "hmm-calm-soxl", "streak-voo-buy-tecl", "markov_model_amd"}
	steps, list, already := Plan("/repo", "hmm-normal-tecl", current, bundled, false)
	live := append(slices.Clone(current), "hmm-normal-tecl")
	if already || !slices.Equal(list, append(slices.Clone(live), "streak-voo-buy-tecl", "markov_model_amd")) {
		t.Fatalf("export list %v already=%v: the live list, the new row, then bundled rows not yet in it", list, already)
	}
	if got := names(steps); !slices.Equal(got, []string{"export", "vendor", "build", "check", "deploy", "allowlist", "restart"}) {
		t.Fatalf("steps %v", got)
	}
	// The bundle is deployed before the list changes, and the list changes before the restart.
	if steps[4].Name != "deploy" || steps[5].Name != "allowlist" || steps[6].Name != "restart" || steps[6].Env[0] != "FORCE_DEPLOY=1" {
		t.Fatalf("order %v", names(steps))
	}
	// The check resolves the list with the new row, in a folder of its own, not the shared one.
	check := steps[3]
	if !slices.Contains(check.Env, "STRATEGY_ALLOWLIST="+strings.Join(live, ",")) || !strings.HasPrefix(check.Env[0], "APP_FOLDER=/repo/data/deploy_check") {
		t.Fatalf("check env %v", check.Env)
	}
	if steps[0].Cmd[1] != strings.Join(list, ",") || steps[0].Cmd[1] == strings.Join(live, ",") {
		t.Fatalf("export gets %q", steps[0].Cmd[1])
	}
}

func TestPlanForARowAlreadyLiveOnlyRefreshesTheBundle(t *testing.T) {
	current := []string{"streak-voo-buy-tecl+markov_model_amd", "hmm-calm-soxl"}
	steps, list, already := Plan("/repo", "markov_model_amd", current, []string{"markov_model_amd"}, true)
	if !already || !slices.Equal(list, current) {
		t.Fatalf("a stack member is live: list %v already=%v", list, already)
	}
	if got := names(steps); !slices.Equal(got, []string{"export", "vendor", "build", "check", "deploy"}) {
		t.Fatalf("steps %v (no allowlist step, and none to restart with -no-restart)", got)
	}
}

func seedRow(t *testing.T, path string) {
	t.Helper()
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct)
		VALUES ('hmm-normal-tecl', 'TECL', 'QQQ', 'TECL', 'normal_filter', 0.5, 8, 0, 0.02, 0.2, 0.045, 0.0005)`); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyARowOfTheStrategiesDatabaseCanBeDeployed(t *testing.T) {
	ref := filepath.Join(t.TempDir(), "strategies.db")
	seedRow(t, ref)
	if table, err := findStrategyAssetRowTable(ref, "hmm-normal-tecl"); err != nil || table != "hmm_strategy" {
		t.Fatalf("table %q err %v", table, err)
	}
	if _, err := findStrategyAssetRowTable(ref, "tsll-daily-one-share"); err == nil {
		t.Fatal("a strategy with its own pipeline is not a row and cannot be exported")
	}
	if _, err := findStrategyAssetRowTable(filepath.Join(t.TempDir(), "missing.db"), "x"); err == nil {
		t.Fatal("a missing strategies database must be reported")
	}
	var out bytes.Buffer
	if err := Run(context.Background(), &out, Options{DryRun: true}); err == nil {
		t.Fatal("an id is required")
	}
	if err := Run(context.Background(), &out, Options{ID: "nope", BacktestDB: ref, DryRun: true}); err == nil {
		t.Fatal("an unknown row must be refused before anything is read from storage")
	}
}

// runStep runs real commands: the working folder, the added environment and the output.
func TestRunStepRunsInItsFolderWithItsEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "step.sh"), []byte("#!/bin/sh\necho \"in $(basename \"$PWD\") with $STEP_VAR\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runStep(context.Background(), &out, Step{Name: "t", Dir: dir, Env: []string{"STEP_VAR=ok"}, Cmd: []string{"./step.sh"}}); err != nil {
		t.Fatal(err)
	}
	if want := "in " + filepath.Base(dir) + " with ok"; !strings.Contains(out.String(), want) {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
	if err := runStep(context.Background(), &out, Step{Name: "bad", Dir: dir, Cmd: []string{"sh", "-c", "exit 3"}}); err == nil {
		t.Fatal("a failing step must return its error")
	}
}

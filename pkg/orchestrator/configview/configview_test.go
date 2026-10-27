package configview

import (
	"strings"
	"testing"
)

func TestVarsMaskSecretsAndKeepSettings(t *testing.T) {
	data := []byte("STRATEGY_ALLOWLIST=a+b\nSCHWAB_APP_SECRET=hunter2\nEMAIL_RECIPIENT=me@example.com\nTZ=America/New_York\nDASHBOARD_PASSWORD=pw\n")
	got := map[string]Var{}
	for _, v := range Vars(data) {
		got[v.Name] = v
	}
	if v := got["STRATEGY_ALLOWLIST"]; v.Secret || v.Value != "a+b" {
		t.Errorf("allowlist %+v", v)
	}
	if v := got["TZ"]; v.Secret || v.Value != "America/New_York" {
		t.Errorf("TZ %+v", v)
	}
	for _, name := range []string{"SCHWAB_APP_SECRET", "EMAIL_RECIPIENT", "DASHBOARD_PASSWORD"} {
		if v := got[name]; !v.Secret || v.Value != Masked || strings.Contains(v.Value, "hunter2") {
			t.Errorf("%s not masked: %+v", name, v)
		}
	}
}

func TestCheckResolvesStacksFromTheBundle(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	good := Check("markov_model_mull+streak-shpu-down3+markov_model_amuu+markov_model_vrtl")
	if len(good) != 1 || !good[0].OK || len(good[0].Members) != 4 {
		t.Fatalf("good stack: %+v", good)
	}
	bad := Check("markov_model_mull+streak-no-such-row,streak-shpu-down3")
	if len(bad) != 2 || bad[0].OK || !strings.Contains(bad[0].Problem, "streak-no-such-row") || !bad[1].OK {
		t.Fatalf("mixed list: %+v", bad)
	}
	if empty := Check(" , "); len(empty) != 1 || empty[0].OK || empty[0].Problem == "" {
		t.Fatalf("empty list: %+v", empty)
	}
}

package config

import "testing"

func TestParseEnv(t *testing.T) {
	keys, v := ParseEnv([]byte("# c\n\nA=1\nB = \"two words\"\nC='x'\nnoequals\nD=a=b\nA=3\n=bad\n"))
	if len(keys) != 4 || keys[0] != "A" || keys[1] != "B" || keys[2] != "C" || keys[3] != "D" {
		t.Fatalf("keys = %v", keys)
	}
	if v["A"] != "3" || v["B"] != "two words" || v["C"] != "x" || v["D"] != "a=b" {
		t.Errorf("values = %v", v)
	}
}

func TestProjectAndBucketNaming(t *testing.T) {
	for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GAE_APPLICATION", "GCS_BUCKET"} {
		t.Setenv(k, "")
	}
	if Project() != DefaultProject || Bucket() != "trade-orchestrator-data-"+DefaultProject {
		t.Errorf("defaults: %s %s", Project(), Bucket())
	}
	t.Setenv("GAE_APPLICATION", "e~other-proj")
	if Project() != "other-proj" || Bucket() != "trade-orchestrator-data-other-proj" {
		t.Errorf("GAE_APPLICATION: %s %s", Project(), Bucket())
	}
	t.Setenv("GCS_BUCKET", "explicit")
	if Bucket() != "explicit" {
		t.Errorf("GCS_BUCKET must win, got %s", Bucket())
	}
}

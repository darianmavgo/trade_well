package strategy_allowlist

import (
	"slices"
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	cur := []string{"a+b", "c"}
	got, err := Apply(cur, "set", []string{"x,y"})
	if err != nil || !slices.Equal(got, []string{"x", "y"}) {
		t.Fatalf("set: %v %v", got, err)
	}
	got, _ = Apply(cur, "add", []string{"c,d"})
	if !slices.Equal(got, []string{"a+b", "c", "d"}) {
		t.Fatalf("add keeps order and skips duplicates: %v", got)
	}
	got, _ = Apply(cur, "remove", []string{"c"})
	if !slices.Equal(got, []string{"a+b"}) {
		t.Fatalf("remove: %v", got)
	}
	if _, err := Apply(cur, "remove", []string{"zzz"}); err == nil {
		t.Fatal("removing an id that is not there must fail")
	}
	if _, err := Apply([]string{"c"}, "remove", []string{"c"}); err == nil {
		t.Fatal("emptying the list must fail")
	}
	if _, err := Apply(cur, "frob", nil); err == nil {
		t.Fatal("unknown action must fail")
	}
}

func TestSetLineKeepsEveryOtherLine(t *testing.T) {
	in := []byte("A=1\n# note\nSTRATEGY_ALLOWLIST=old\nB=2\n")
	got := string(SetLine(in, "new1,new2"))
	if got != "A=1\n# note\nSTRATEGY_ALLOWLIST=new1,new2\nB=2\n" {
		t.Fatalf("replace: %q", got)
	}
	got = string(SetLine([]byte("A=1\n"), "x"))
	if !strings.HasSuffix(got, "STRATEGY_ALLOWLIST=x\n") || !strings.HasPrefix(got, "A=1\n") {
		t.Fatalf("append: %q", got)
	}
}

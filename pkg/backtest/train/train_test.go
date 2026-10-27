package train

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunDispatchesFamilies(t *testing.T) {
	for _, fam := range []string{"streak", "hold"} {
		var out, errb bytes.Buffer
		if code := Run([]string{fam}, &out, &errb); code != 0 || !strings.Contains(out.String(), "nothing to train") {
			t.Fatalf("%s: code %d out %q err %q", fam, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"nope"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "unknown family") || !strings.Contains(errb.String(), "markov") {
		t.Fatalf("unknown family: code %d err %q", code, errb.String())
	}
	if code := Run(nil, &out, &errb); code != 2 {
		t.Fatalf("no args: code %d", code)
	}
}

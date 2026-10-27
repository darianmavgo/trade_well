package validate

import (
	"bytes"
	"strings"
	"testing"
)

func TestFoldsNeedAStrategy(t *testing.T) {
	for _, args := range [][]string{{}, {"walk"}} {
		var out bytes.Buffer
		err := run(args, &out)
		if err == nil || !strings.Contains(err.Error(), "-strategy is required") {
			t.Errorf("run(%v) = %v, want the -strategy error", args, err)
		}
	}
}

func TestVerdictAloneNeedsAnEarlierRun(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"verdict", "-run-id", "99999999"}, &out)
	if err == nil || !strings.Contains(err.Error(), "no run 99999999") {
		t.Errorf("got %v, want an error naming the missing run", err)
	}
}

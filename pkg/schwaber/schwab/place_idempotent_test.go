package schwab

import (
	"testing"
)

func TestEnsureClientOrderIDPreservesCaller(t *testing.T) {
	o := &Order{ClientOrderID: "co_caller"}
	if got := EnsureClientOrderID("h", o); got != "co_caller" {
		t.Fatalf("got %s", got)
	}
}

package trader

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

func TestFilterLinkedAccounts(t *testing.T) {
	linked := []schwab.LinkedAccount{
		{AccountNumber: "25474194", HashValue: "hash-roth"},
		{AccountNumber: "74234603", HashValue: "hash-ind"},
		{AccountNumber: "99999999", HashValue: "hash-other"},
	}
	got, err := FilterLinkedAccounts(linked, []string{"25474194", "74234603"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].AccountNumber != "25474194" || got[1].AccountNumber != "74234603" {
		t.Fatalf("got %+v", got)
	}

	_, err = FilterLinkedAccounts(linked, []string{"25474194", "nope"})
	if err == nil {
		t.Fatalf("expected missing-account error, got nil")
	}
}

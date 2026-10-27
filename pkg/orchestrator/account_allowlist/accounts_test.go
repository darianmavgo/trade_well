package account_allowlist

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse(" 111 ,222,,111,333 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"111", "222", "333"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
	if _, err := Parse(" , , "); err == nil {
		t.Fatal("expected error for all-blank input, got nil")
	}
}

func TestParseExceedsCap(t *testing.T) {
	_, err := Parse("1,2,3,4,5,6")
	if err == nil {
		t.Fatal("expected error for 6 accounts, got nil")
	}
}

func TestParseAtCap(t *testing.T) {
	got, err := Parse("1,2,3,4,5")
	if err != nil {
		t.Fatalf("unexpected error at cap: %v", err)
	}
	if len(got) != MaxAccounts {
		t.Fatalf("got %d accounts, want %d", len(got), MaxAccounts)
	}
}

func TestFromEnvReadsAccountNumbersList(t *testing.T) {
	t.Setenv(EnvAccountNumbers, "25474194,74234603,75124906")
	got, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"25474194", "74234603", "75124906"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFromEnvFailsWithoutAccountNumbers(t *testing.T) {
	t.Setenv(EnvAccountNumbers, "")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), EnvAccountNumbers) {
		t.Fatalf("want an error naming %s, got %v", EnvAccountNumbers, err)
	}
}

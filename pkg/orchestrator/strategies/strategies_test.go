package strategies

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse(" sig-voo-buy-tecl ,rsi2,,rsi2,gld-decline ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"sig-voo-buy-tecl", "rsi2", "gld-decline"}
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

func TestContains(t *testing.T) {
	list := []string{"sig-voo-buy-tecl", "rsi2"}
	if !slices.Contains(list, "rsi2") {
		t.Error("expected list to contain rsi2")
	}
	if slices.Contains(list, "bb-capitulation") {
		t.Error("expected list to not contain bb-capitulation")
	}
}

func TestParseKeepsStackID(t *testing.T) {
	got, err := Parse("sig-voo-buy-tecl+mara_tree+pdd_tree")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "sig-voo-buy-tecl+mara_tree+pdd_tree" {
		t.Fatalf("stack id must stay one allowlist entry, got %v", got)
	}
	got, err = Parse("sig-voo-buy-tecl+mara_tree+pdd_tree, rsi2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "sig-voo-buy-tecl+mara_tree+pdd_tree" || got[1] != "rsi2" {
		t.Fatalf("comma splits stacks from other ids, got %v", got)
	}
}

func TestBundleListsTheEmbeddedRows(t *testing.T) {
	info, err := ReadBundledStrategyAssetInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Hash) != 10 || len(info.IDs) == 0 {
		t.Fatalf("bundle %+v: want a 10-character hash and at least one id", info)
	}
	if !slices.Contains(info.IDs, "streak-shpu-down3") {
		t.Fatalf("bundle ids %v do not include streak-shpu-down3", info.IDs)
	}
	// The bundle is exported at the backtest's flat allocation, and says so.
	if info.Alloc != 0.20 {
		t.Fatalf("bundle allocation = %v, want 0.20 (re-export with scripts/export_strategies.sh)", info.Alloc)
	}
}

func TestMembersExpandsStacksWithoutRepeats(t *testing.T) {
	got := Members([]string{"a+b+c", "tsll", "b", "d+a"})
	want := []string{"a", "b", "c", "tsll", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

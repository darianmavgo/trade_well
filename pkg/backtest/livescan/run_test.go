package livescan

import (
	"context"
	"testing"
)

func TestRunRequiresDB(t *testing.T) {
	if _, err := Run(context.Background(), Config{Strategy: "streak-voo-buy-tecl"}); err == nil {
		t.Fatal("expected error for empty DB")
	}
}

func TestRunPlusStackResolvesEachMember(t *testing.T) {
	// Unknown member proves "+" is split and each part is looked up; no
	// network or DB access happens before resolution fails.
	_, err := Run(context.Background(), Config{DB: t.TempDir() + "/m.db", Strategy: "streak-voo-buy-tecl+no_such_strategy"})
	if err == nil {
		t.Fatal("expected unknown-strategy error")
	}
}

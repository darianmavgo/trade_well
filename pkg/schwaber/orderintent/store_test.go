package orderintent

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBeginAckFailGetList(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "intents.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	in := Intent{
		ClientOrderID: "co_test1",
		AccountHash:   "hashA",
		Symbol:        "AAPL",
		Instruction:   "BUY",
		Quantity:      10,
		RequestJSON:   `{"orderType":"LIMIT"}`,
	}
	if err := store.BeginIntent(ctx, in); err != nil {
		t.Fatalf("BeginIntent: %v", err)
	}
	err = store.BeginIntent(ctx, in)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
	got, err := store.GetIntent(ctx, "co_test1")
	if err != nil || got == nil {
		t.Fatalf("GetIntent: %v %#v", err, got)
	}
	if got.Status != StatusPending {
		t.Fatalf("status=%s", got.Status)
	}

	if err := store.AckIntent(ctx, "co_test1", "12345"); err != nil {
		t.Fatalf("AckIntent: %v", err)
	}
	got, _ = store.GetIntent(ctx, "co_test1")
	if got.Status != StatusAcked || got.BrokerOrderID != "12345" {
		t.Fatalf("after ack: %+v", got)
	}

	// Failed intent can be restarted.
	in2 := Intent{ClientOrderID: "co_fail", AccountHash: "h", Symbol: "X", Instruction: "BUY", Quantity: 1}
	_ = store.BeginIntent(ctx, in2)
	_ = store.FailIntent(ctx, "co_fail", "rejected")
	if err := store.BeginIntent(ctx, in2); err != nil {
		t.Fatalf("restart failed intent: %v", err)
	}
	got, _ = store.GetIntent(ctx, "co_fail")
	if got.Status != StatusPending {
		t.Fatalf("expected pending after restart, got %s", got.Status)
	}

	_ = store.MarkUnknown(ctx, "co_fail", "network")
	list, err := store.ListUnresolved(ctx)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(list) != 1 || list[0].ClientOrderID != "co_fail" {
		t.Fatalf("unresolved=%+v", list)
	}
}

func TestConcurrentBeginOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "race.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	in := Intent{ClientOrderID: "co_race", AccountHash: "h", Symbol: "MARA", Instruction: "BUY", Quantity: 1}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.BeginIntent(ctx, in)
		}()
	}
	wg.Wait()
	close(errs)
	var ok, exists int
	for e := range errs {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrAlreadyExists) {
			exists++
		} else {
			t.Errorf("unexpected err: %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("expected exactly 1 successful BeginIntent, got %d (exists=%d)", ok, exists)
	}
}

func TestDeterministicClientOrderIDStable(t *testing.T) {
	a := DeterministicClientOrderID("74234603", "mara", "buy", 100, "mara_tree", "2026-09-21")
	b := DeterministicClientOrderID("74234603", "MARA", "BUY", 100, "mara_tree", "2026-09-21")
	if a != b {
		t.Fatalf("not stable: %s vs %s", a, b)
	}
	c := DeterministicClientOrderID("74234603", "MARA", "BUY", 101, "mara_tree", "2026-09-21")
	if a == c {
		t.Fatal("qty change should change id")
	}
	if len(a) < 10 || a[:3] != "co_" {
		t.Fatalf("unexpected format %q", a)
	}
}

func TestFromStagedKeyIncludesID(t *testing.T) {
	a := FromStagedKey("acc", "AAPL", "BUY", "2026-09-21", 1)
	b := FromStagedKey("acc", "AAPL", "BUY", "2026-09-21", 2)
	if a == b {
		t.Fatal("different staged ids must differ")
	}
}

type fakeLister struct {
	orders map[string][]BrokerOrder
	err    error
}

func (f *fakeLister) ListAccountOrdersForReconcile(ctx context.Context, accountHash string, from, to time.Time) ([]BrokerOrder, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.orders[accountHash], nil
}

func TestReconcilePendingIntents(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "rec.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	created := time.Now().UTC().Add(-time.Minute)
	in := Intent{
		ClientOrderID: "co_rec",
		AccountHash:   "hashR",
		Symbol:        "AAPL",
		Instruction:   "BUY",
		Quantity:      5,
		CreatedAt:     created,
	}
	if err := store.BeginIntent(ctx, in); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// Overwrite created_at for matching window (BeginIntent sets now).
	_, _ = store.db.Exec(`UPDATE order_intents SET created_at=? WHERE client_order_id=?`,
		created.Format(time.RFC3339Nano), "co_rec")

	lister := &fakeLister{orders: map[string][]BrokerOrder{
		"hashR": {{
			OrderID: "999", Symbol: "AAPL", Instruction: "BUY", Quantity: 5,
			EnteredAt: created.Add(5 * time.Second), Status: "WORKING",
		}},
	}}
	res, err := ReconcilePendingIntents(ctx, store, lister)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Acked != 1 {
		t.Fatalf("expected 1 acked, got %+v", res)
	}
	got, _ := store.GetIntent(ctx, "co_rec")
	if got.Status != StatusAcked || got.BrokerOrderID != "999" {
		t.Fatalf("got %+v", got)
	}
}

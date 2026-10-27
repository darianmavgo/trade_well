package clear_older

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunKeepsNewestAndSiblings(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"w.db", "w.db-wal", "w.db-shm", "w_2.db", "w_3.db", "w_4.db-wal", "w_4.db",
		"w_5.db", "w_5.db-wal", "w_6.db", "w-inverse.db", "w-inverse_2.db",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Run(Config{Keep: []string{filepath.Join(dir, "w_5.db")}, Workers: 100})
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("removed %d, want 7", n)
	}
	for _, n := range []string{"w_5.db", "w_5.db-wal", "w_6.db", "w-inverse.db", "w-inverse_2.db"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s should survive: %v", n, err)
		}
	}
	for _, n := range []string{"w.db", "w.db-wal", "w_2.db", "w_4.db"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s should be gone", n)
		}
	}
}

func TestMissingKeepFails(t *testing.T) {
	if _, err := Run(Config{Keep: []string{filepath.Join(t.TempDir(), "w_5.db")}}); err == nil {
		t.Fatal("want error")
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.db", "a_2.db", "a_10.db", "a_3.db", "b.db", "c_2.db"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a_10.db", "b.db", "c_2.db"}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if filepath.Base(got[i]) != w {
			t.Errorf("got %v want %v", got, want)
		}
	}
}

func TestStale(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-40 * 24 * time.Hour)
	mk := func(rel string, mt time.Time) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mt, mt)
	}
	mk("a.db", old)
	mk("a.db-wal", time.Now()) // a live wal keeps the group fresh
	mk("b.db", old)
	mk("7/stack.db", old)
	mk("8/stack.db", old)
	mk("8/oos/x.db", time.Now())
	mk("notes/x.db", old) // not a numbered run folder
	os.Chtimes(filepath.Join(dir, "7"), old, old)
	got, err := Stale(dir, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0]) != "7" || filepath.Base(got[1]) != "b.db" {
		t.Fatalf("got %v", got)
	}
	n, err := Run(Config{Dir: dir, Stale: 30 * 24 * time.Hour})
	if err != nil || n != 2 {
		t.Fatalf("run: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "7")); err == nil {
		t.Error("7 should be gone")
	}
}

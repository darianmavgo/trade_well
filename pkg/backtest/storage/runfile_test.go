package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunFile(t *testing.T) {
	root := t.TempDir()
	if _, _, err := RunFile(root, 0, false, "x.db"); err == nil {
		t.Fatal("reading with no runs should fail")
	}
	p1, id1, err := RunFile(root, 0, true, "x.db")
	if err != nil || id1 != 1 || p1 != filepath.Join(root, "1", "x.db") {
		t.Fatalf("create: %s %d %v", p1, id1, err)
	}
	p2, id2, err := RunFile(root, 0, true, "x.db")
	if err != nil || id2 != 2 {
		t.Fatalf("second create: %s %d %v", p2, id2, err)
	}
	if p, id, err := RunFile(root, 0, false, "x.db"); err != nil || id != 2 || p != p2 {
		t.Fatalf("latest: %s %d %v", p, id, err)
	}
	if p, id, err := RunFile(root, 1, true, "y.db"); err != nil || id != 1 || p != filepath.Join(root, "1", "y.db") {
		t.Fatalf("explicit run: %s %d %v", p, id, err)
	}
	if _, _, err := RunFile(root, 9, true, "x.db"); err == nil {
		t.Fatal("a run that does not exist should fail")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 2 {
		t.Fatalf("%d run folders, want 2", len(entries))
	}
}

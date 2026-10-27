package stratlist

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func tempDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE s (id TEXT, kind TEXT);
		INSERT INTO s VALUES ('b','x'),('a','x'),('a','x'),('c','y'),(NULL,'x'),('  ','x')`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSQL(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "q.sql")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListDedupesAndSkipsBlanks(t *testing.T) {
	ids, err := List(context.Background(), Config{DB: tempDB(t), SQLFile: writeSQL(t, "SELECT id FROM s WHERE kind='x';")})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v want %v", ids, want)
	}
}

func TestListIsReadOnly(t *testing.T) {
	db := tempDB(t)
	if _, err := List(context.Background(), Config{DB: db, SQLFile: writeSQL(t, "DELETE FROM s")}); err == nil {
		// DELETE returns no rows; the pragma must have blocked it either way.
		ids, _ := List(context.Background(), Config{DB: db, SQLFile: writeSQL(t, "SELECT id FROM s")})
		if len(ids) == 0 {
			t.Fatal("DELETE ran")
		}
	}
}

func TestListMissingDB(t *testing.T) {
	if _, err := List(context.Background(), Config{DB: filepath.Join(t.TempDir(), "nope.db"), SQLFile: writeSQL(t, "SELECT 1")}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunComma(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"-db", tempDB(t), "-comma", writeSQL(t, "SELECT id FROM s WHERE kind='x'")}, &out, &errb)
	if code != 0 || strings.TrimSpace(out.String()) != "b,a" {
		t.Fatalf("code %d out %q err %q", code, out.String(), errb.String())
	}
}

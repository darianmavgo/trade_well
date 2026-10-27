package dbutil

import "testing"

func TestTursoDSN(t *testing.T) {
	got, err := tursoDSN("libsql://trade-me.turso.io", "tok")
	if err != nil || got != "https://trade-me.turso.io?authToken=tok" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := tursoDSN("libsql://trade-me.turso.io", ""); err == nil {
		t.Error("an empty token must be an error")
	}
	if _, err := tursoDSN("not a url", "tok"); err == nil {
		t.Error("a URL without a host must be an error")
	}
}

func TestTursoIsOffUnderGoTest(t *testing.T) {
	t.Setenv(EnvTursoURL, "libsql://x.turso.io")
	t.Setenv(EnvTursoToken, "tok")
	if _, ok, err := Turso(); ok || err != nil {
		t.Fatalf("Turso() = ok %v, err %v; tests must always use local files", ok, err)
	}
}

func TestParseTimeReadsTheFormsSQLiteStores(t *testing.T) {
	for _, in := range []string{"2026-10-15 20:15:00", "2026-10-15T20:15:00Z", "2026-10-15 20:15:00.123456789+00:00", "2026-10-15 20:15:00.600257727 +0000 UTC", "2026-10-15 20:15:00.6 +0000 UTC m=+12.5"} {
		got, ok := parseTime(in)
		if !ok || got.Year() != 2026 || got.Hour() != 20 {
			t.Errorf("parseTime(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := parseTime("MULL"); ok {
		t.Error("a symbol is not a time")
	}
}

func TestEnsureOnceRunsEveryTimeOnALocalFile(t *testing.T) {
	n := 0
	for i := 0; i < 3; i++ {
		if err := EnsureOnce("k", func() error { n++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n != 3 {
		t.Errorf("ran %d times; a local file must run the setup every call", n)
	}
}

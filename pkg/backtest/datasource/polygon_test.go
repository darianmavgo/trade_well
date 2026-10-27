package datasource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePolygonTimeframe(t *testing.T) {
	tests := []struct {
		input        string
		expectedMult int
		expectedSpan string
	}{
		{"1d", 1, "day"},
		{"day", 1, "day"},
		{"", 1, "day"},
		{"1h", 1, "hour"},
		{"hour", 1, "hour"},
		{"5m", 5, "minute"},
		{"15m", 15, "minute"},
		{"1m", 1, "minute"},
		{"1w", 1, "week"},
		{"week", 1, "week"},
	}

	for _, tt := range tests {
		mult, span := parsePolygonTimeframe(tt.input)
		if mult != tt.expectedMult || span != tt.expectedSpan {
			t.Errorf("parsePolygonTimeframe(%q) = (%d, %q), expected (%d, %q)",
				tt.input, mult, span, tt.expectedMult, tt.expectedSpan)
		}
	}
}

func TestResolvePolygonAPIKey_EnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	envContent := `
# Sample comment
POLYGON_API_KEY="test_key_from_env"
SOME_OTHER_VAR=123
`
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("failed to write temp .env: %v", err)
	}

	envMap, err := parseSimpleEnvFile(envPath)
	if err != nil {
		t.Fatalf("failed to parse env file: %v", err)
	}

	if envMap["POLYGON_API_KEY"] != "test_key_from_env" {
		t.Errorf("expected test_key_from_env, got %s", envMap["POLYGON_API_KEY"])
	}
}

func TestPolygonFetchRetriesAfter429AndSpacesCalls(t *testing.T) {
	var calls []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, time.Now())
		if len(calls) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"status":"OK","results":[{"o":1,"h":2,"l":0.5,"c":1.5,"v":10,"t":1767632400000}]}`)
	}))
	defer srv.Close()

	p := NewPolygonDataSource("k", srv.Client())
	p.BaseURL = srv.URL
	p.MinInterval = 30 * time.Millisecond
	p.RetryWait = 50 * time.Millisecond
	bars, err := p.Fetch(context.Background(), FetchRequest{
		Symbol: "SPY", Timeframe: "1h",
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(bars) != 1 || bars[0].Date != "2026-01-05 17:00:00" {
		t.Fatalf("bars = %+v", bars)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (429 then retry)", len(calls))
	}
	if gap := calls[1].Sub(calls[0]); gap < 50*time.Millisecond {
		t.Errorf("retry came after %v, want at least RetryWait", gap)
	}
}

func TestPolygonSetRate(t *testing.T) {
	p := &PolygonDataSource{}
	p.SetRate(5)
	if p.MinInterval != 12*time.Second {
		t.Errorf("5/min = %v, want 12s", p.MinInterval)
	}
	p.SetRate(0)
	if p.MinInterval != 0 {
		t.Errorf("0/min = %v, want unthrottled", p.MinInterval)
	}
}

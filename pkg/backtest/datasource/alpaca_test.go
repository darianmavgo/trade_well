package datasource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAlpacaFetchPagesAndHeaders(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("APCA-API-KEY-ID") != "id" || r.Header.Get("APCA-API-SECRET-KEY") != "sec" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("page_token") == "" {
			fmt.Fprint(w, `{"bars":[{"t":"2026-01-05T14:00:00Z","o":1,"h":2,"l":0.5,"c":1.5,"v":10}],"next_page_token":"p2"}`)
			return
		}
		fmt.Fprint(w, `{"bars":[{"t":"2026-01-05T15:00:00Z","o":1.5,"h":2,"l":1,"c":1.8,"v":20}],"next_page_token":null}`)
	}))
	defer srv.Close()

	a := NewAlpacaDataSource("id", "sec", "", srv.Client())
	a.BaseURL = srv.URL
	bars, err := a.Fetch(context.Background(), FetchRequest{
		Symbol: "spy", Timeframe: "1h",
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(bars) != 2 || bars[0].Date != "2026-01-05 14:00:00" || bars[1].Volume != 20 || bars[0].Symbol != "SPY" || bars[0].Timeframe != "1h" {
		t.Fatalf("bars = %+v", bars)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2 pages", len(queries))
	}
}

func TestAlpacaTimeframe(t *testing.T) {
	for in, want := range map[string]string{"1h": "1Hour", "2h": "2Hour", "5m": "5Min", "1d": "1Day", "": "1Day"} {
		if got := alpacaTimeframe(in); got != want {
			t.Errorf("alpacaTimeframe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlpacaFeedDefaultsToSIP(t *testing.T) {
	t.Setenv("ALPACA_FEED", "")
	if a := NewAlpacaDataSource("id", "sec", "", nil); a.Feed != "sip" {
		t.Errorf("default feed = %q, want sip", a.Feed)
	}
	if a := NewAlpacaDataSource("id", "sec", "IEX", nil); a.Feed != "iex" {
		t.Errorf("explicit feed = %q, want iex", a.Feed)
	}
}

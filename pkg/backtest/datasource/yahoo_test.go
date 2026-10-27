package datasource

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func yahooFixture(body string) *YahooDataSource {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	return NewYahooDataSource(client)
}

func fetchFixture(t *testing.T, body string) []string {
	t.Helper()
	bars, err := yahooFixture(body).Fetch(context.Background(), FetchRequest{
		Symbol: "VOO", StartDate: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), Timeframe: "1d",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bars {
		got = append(got, b.Date)
	}
	return got
}

// 2026-09-21 and 2026-09-23 13:30 UTC session opens; 09-23 close is null.
// regularMarketTime 1790193600 = 2026-09-23 20:00 UTC = 16:00 EDT.
const yahooNullLastClose = `{"chart":{"result":[{"meta":{"symbol":"VOO","regularMarketPrice":%PRICE%,"regularMarketTime":%TIME%},
"timestamp":[1789997400,1790170200],
"indicators":{"quote":[{"open":[706.19,712.13],"high":[713.0,712.37],"low":[705.0,706.36],"close":[712.78,null],"volume":[8859800,13785872]}],
"adjclose":[{"adjclose":[712.78,null]}]}}],"error":null}}`

func TestYahooFillsNullCloseOfJustClosedSession(t *testing.T) {
	body := strings.NewReplacer("%PRICE%", "707.6", "%TIME%", "1790193600").Replace(yahooNullLastClose)
	bars, err := yahooFixture(body).Fetch(context.Background(), FetchRequest{Symbol: "VOO", Timeframe: "1d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("got %d bars, want 2 (last bar's null close should be settled from regularMarketPrice)", len(bars))
	}
	if last := bars[1]; last.Date != "2026-09-23" || last.Close != 707.6 || last.AdjClose != 707.6 {
		t.Errorf("last bar = %+v, want 2026-09-23 close/adj 707.6", last)
	}
}

func TestYahooKeepsSkippingNullCloseWhenNotSafe(t *testing.T) {
	cases := map[string]string{
		"session still open (15:59 ET)": strings.NewReplacer("%PRICE%", "707.6", "%TIME%", "1790193540").Replace(yahooNullLastClose),
		"price outside high/low":        strings.NewReplacer("%PRICE%", "800.0", "%TIME%", "1790193600").Replace(yahooNullLastClose),
		"market time on another day":    strings.NewReplacer("%PRICE%", "707.6", "%TIME%", "1790107200").Replace(yahooNullLastClose),
		"no price":                      strings.NewReplacer("%PRICE%", "0", "%TIME%", "1790193600").Replace(yahooNullLastClose),
	}
	for name, body := range cases {
		if got := fetchFixture(t, body); len(got) != 1 || got[0] != "2026-09-21" {
			t.Errorf("%s: bars = %v, want only 2026-09-21", name, got)
		}
	}
}

func TestSettledCloseFromMetaOnlyForLastBar(t *testing.T) {
	h, l := 712.37, 706.36
	if _, ok := settledCloseFromMeta(false, 1790170200, &h, &l, 707.6, 1790193600); ok {
		t.Error("a null close on a non-final bar is a data gap and must not be filled")
	}
}

package schwab

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestNewAPIErrorKeepsLegacyTextAndAddsContext(t *testing.T) {
	resp := &http.Response{StatusCode: 400, Header: http.Header{}}
	resp.Header.Set("Schwab-Client-CorrelId", "abc-123")
	e := newAPIError(http.MethodPost, "https://api.schwabapi.com/trader/v1/accounts/SECRETHASH/orders?x=1", resp,
		[]byte(`{"message":"A validation error occurred"}`), `{"orderType":"LIMIT"}`)

	if !strings.HasPrefix(e.Error(), `API error: status 400, body: {"message":"A validation error occurred"}`) {
		t.Errorf("legacy prefix changed: %q", e.Error())
	}
	if !strings.Contains(e.Error(), "schwab_correl_id=abc-123") {
		t.Errorf("Error() missing correlation id: %q", e.Error())
	}
	if strings.Contains(e.Error(), "orderType") {
		t.Errorf("Error() must not carry the request JSON (string matchers read it): %q", e.Error())
	}
	d := e.Detail()
	for _, want := range []string{"POST /trader/v1/accounts/{hash}/orders", "HTTP 400", "correl_id=abc-123", `request={"orderType":"LIMIT"}`} {
		if !strings.Contains(d, want) {
			t.Errorf("Detail() missing %q: %s", want, d)
		}
	}
	if strings.Contains(d, "SECRETHASH") || strings.Contains(d, "x=1") {
		t.Errorf("Detail() leaked the account hash or query: %s", d)
	}
}

func TestIsAmbiguousPlaceErrorUsesStatusForAPIErrors(t *testing.T) {
	// The body says "unknown" and "timeout", which the text matcher treats as
	// ambiguous; a 400 is a definite refusal.
	refused := &APIError{Status: 400, Body: `{"message":"unknown timeout eof"}`}
	if isAmbiguousPlaceError(refused) {
		t.Error("a 400 must not be ambiguous")
	}
	if !isAmbiguousPlaceError(&APIError{Status: 503, Body: "busy"}) {
		t.Error("a 503 must be ambiguous")
	}
	if isAmbiguousPlaceError(errors.New("some other failure")) {
		t.Error("unrelated error should not be ambiguous")
	}
}

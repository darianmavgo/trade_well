package schwab

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// correlIDHeader is the response header Schwab support asks for when an order
// is refused: it identifies the request on their side.
const correlIDHeader = "Schwab-Client-CorrelId"

// APIError is a non-2xx answer from the Schwab API. Error() keeps the
// long-standing "API error: status N, body: ..." text (callers and stored
// intents match on it) and adds the correlation ID; everything else is a field,
// so a refused order can be explained without re-deriving what was sent.
type APIError struct {
	Method      string // HTTP method
	Endpoint    string // path only: no query string, and the account hash is elided
	Status      int
	Body        string // Schwab's response body, verbatim
	CorrelID    string // Schwab-Client-CorrelId response header, "" when absent
	RequestJSON string // the order JSON that was POSTed; set for order placement only
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("API error: status %d, body: %s", e.Status, e.Body)
	if e.CorrelID != "" {
		s += fmt.Sprintf(" (schwab_correl_id=%s)", e.CorrelID)
	}
	return s
}

// Detail is the full one-line-per-fact account of the failure for logs and the
// audit trail: what was called, what Schwab answered, and what was sent.
func (e *APIError) Detail() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s -> HTTP %d", e.Method, e.Endpoint, e.Status)
	if e.CorrelID != "" {
		fmt.Fprintf(&sb, " correl_id=%s", e.CorrelID)
	}
	fmt.Fprintf(&sb, " response=%s", e.Body)
	if e.RequestJSON != "" {
		fmt.Fprintf(&sb, " request=%s", e.RequestJSON)
	}
	return sb.String()
}

// newAPIError builds the error for a failed response. rawURL is reduced to its
// path with any account hash replaced, so nothing account-identifying is kept.
func newAPIError(method, rawURL string, resp *http.Response, body []byte, requestJSON string) *APIError {
	endpoint := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		parts := strings.Split(u.Path, "/")
		for i := 1; i < len(parts); i++ {
			if parts[i-1] == "accounts" && parts[i] != "accountNumbers" && parts[i] != "" {
				parts[i] = "{hash}"
			}
		}
		endpoint = strings.Join(parts, "/")
	}
	return &APIError{
		Method:      method,
		Endpoint:    endpoint,
		Status:      resp.StatusCode,
		Body:        string(body),
		CorrelID:    resp.Header.Get(correlIDHeader),
		RequestJSON: requestJSON,
	}
}

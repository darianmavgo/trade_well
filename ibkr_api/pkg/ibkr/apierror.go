package ibkr

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// APIError is a non-2xx answer from the IBKR API, with schwab.APIError's fields and its
// long-standing "API error: status N, body: ..." text. IBKR has no correlation-id
// header, so CorrelID stays empty (the field exists so callers read both alike).
type APIError struct {
	Method      string // HTTP method
	Endpoint    string // path only: no query string, account ids elided
	Status      int
	Body        string // IBKR's response body, verbatim
	CorrelID    string
	RequestJSON string // the order JSON that was POSTed; set for order placement only
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("API error: status %d, body: %s", e.Status, e.Body)
	if e.CorrelID != "" {
		s += fmt.Sprintf(" (correl_id=%s)", e.CorrelID)
	}
	return s
}

// Detail is the full account of the failure for logs and the audit trail.
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

var accountIDSegment = regexp.MustCompile(`^[A-Z]{1,2}[0-9]{5,}$`)

func newAPIError(method, rawURL string, resp *http.Response, body []byte, requestJSON string) *APIError {
	endpoint := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		parts := strings.Split(u.Path, "/")
		for i, p := range parts {
			if accountIDSegment.MatchString(p) {
				parts[i] = "{account}"
			}
		}
		endpoint = strings.Join(parts, "/")
	}
	return &APIError{Method: method, Endpoint: endpoint, Status: resp.StatusCode, Body: string(body), RequestJSON: requestJSON}
}

package schwab

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Read requests (GET) that hit a rate limit or a transient gateway error are retried with
// backoff. Order placement is never retried here: a POST whose outcome is unknown is settled by
// the order-intent store (ErrAmbiguous, ReconcilePendingIntents), not by sending it again.
var (
	retryBackoff  = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 4 * time.Second}
	maxRetryAfter = 10 * time.Second
)

// retryableStatus reports whether a response is worth asking for again.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryDelay is how long to wait before attempt number attempt+1 (attempt counts from 0): the
// server's Retry-After seconds when it sent a sensible one (capped), otherwise the backoff step.
func retryDelay(attempt int, retryAfter string) time.Duration {
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
		d := time.Duration(secs) * time.Second
		if d > maxRetryAfter {
			d = maxRetryAfter
		}
		return d
	}
	if attempt >= len(retryBackoff) {
		attempt = len(retryBackoff) - 1
	}
	return retryBackoff[attempt]
}

// doGetWithRetry sends a bodiless GET, asking again after a 429 or a 502/503/504 until the
// backoff schedule runs out, and returns the last response either way. The caller closes the body.
// It stops early when ctx is cancelled (the killswitch cancels contexts).
func (c *Client) doGetWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.httpClient.Do(req.Clone(ctx))
		if err != nil || !retryableStatus(resp.StatusCode) || attempt >= len(retryBackoff) {
			return resp, err
		}
		wait := retryDelay(attempt, resp.Header.Get("Retry-After"))
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

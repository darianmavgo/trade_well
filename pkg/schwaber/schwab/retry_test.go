package schwab

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryableStatus(t *testing.T) {
	for code, want := range map[int]bool{
		200: false, 400: false, 401: false, 404: false, 500: false,
		429: true, 502: true, 503: true, 504: true,
	} {
		if got := retryableStatus(code); got != want {
			t.Errorf("retryableStatus(%d) = %v, want %v", code, got, want)
		}
	}
	if retryableStatus(http.StatusInternalServerError) {
		t.Errorf("a 500 may mean the request half-ran; it is not retried")
	}
}

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		attempt    int
		retryAfter string
		want       time.Duration
	}{
		{0, "", 500 * time.Millisecond},
		{1, "", 1500 * time.Millisecond},
		{2, "", 4 * time.Second},
		{9, "", 4 * time.Second},             // past the schedule: the last step
		{0, "2", 2 * time.Second},            // the server's Retry-After wins
		{0, "600", maxRetryAfter},            // but is capped
		{1, "soon", 1500 * time.Millisecond}, // an unusable header falls back
		{0, "0", 500 * time.Millisecond},
	}
	for _, c := range cases {
		if got := retryDelay(c.attempt, c.retryAfter); got != c.want {
			t.Errorf("retryDelay(%d, %q) = %v, want %v", c.attempt, c.retryAfter, got, c.want)
		}
	}
}

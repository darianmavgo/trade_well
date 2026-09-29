package oauth1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Session keeps a live session token for Keys and signs requests with it, getting a new
// token when the current one is missing or about to expire. Safe for concurrent use.
type Session struct {
	Keys    *Keys
	BaseURL string       // https://api.ibkr.com
	HTTP    *http.Client // nil = http.DefaultClient

	mu  sync.Mutex
	lst *SessionToken
}

// Token returns the current live session token (nil before the first Establish).
func (s *Session) Token() *SessionToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lst
}

// Restore installs a stored token (from the token file) so a restart need not renegotiate.
func (s *Session) Restore(t *SessionToken) {
	s.mu.Lock()
	s.lst = t
	s.mu.Unlock()
}

// valid reports whether the token is usable for at least another minute.
func (s *Session) valid(now time.Time) bool {
	return s.lst != nil && len(s.lst.Token) > 0 && now.Add(time.Minute).Before(s.lst.Expires)
}

// Ensure returns a valid token, negotiating a new one (Establish) when needed.
func (s *Session) Ensure(ctx context.Context) (*SessionToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.valid(time.Now()) {
		return s.lst, nil
	}
	t, err := s.establish(ctx)
	if err != nil {
		return nil, err
	}
	s.lst = t
	return t, nil
}

// Establish always negotiates a new live session token.
func (s *Session) Establish(ctx context.Context) (*SessionToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.establish(ctx)
	if err != nil {
		return nil, err
	}
	s.lst = t
	return t, nil
}

func (s *Session) establish(ctx context.Context) (*SessionToken, error) {
	req, err := s.Keys.NewLSTRequest(s.BaseURL, time.Now())
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Authorization", req.Header)
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("User-Agent", "ibkr_api-go/1.0")
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("live_session_token: HTTP %d: %s", resp.StatusCode, body)
	}
	var lr LSTResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("live_session_token: %w (body: %.200s)", err, body)
	}
	return s.Keys.LiveSessionToken(lr, req.A)
}

// Authorization returns the Authorization header for a request to rawURL (its query, if
// any, is signed), first making sure a live session token exists.
func (s *Session) Authorization(ctx context.Context, method, rawURL string) (string, error) {
	t, err := s.Ensure(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return s.Keys.Sign(method, rawURL, u.Query(), t, time.Now())
}

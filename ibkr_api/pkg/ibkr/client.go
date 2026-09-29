// Package ibkr is the Interactive Brokers twin of schwaber/pkg/schwab: one Client that
// authenticates, keeps its credentials fresh, sends every request, and turns failures
// into *APIError. Method names, options, errors and the shapes they return follow
// schwaber's, so a caller written against one reads the same against the other.
//
// Two ways in, chosen by configuration (see NewClient):
//   - Client Portal Gateway (no IBKR_CLIENT_ID): the session you log into in a browser
//     at the gateway (default https://localhost:5000, IBKR's own local proxy). This is
//     what an individual account with a username and password uses.
//   - OAuth (IBKR_CLIENT_ID + an RSA key registered with IBKR): tokens from
//     /oauth2/api/v1/token, or from /gw/api/v1/sso-sessions when IBKR_CREDENTIAL is set.
//     Tokens are stored in a token file and synced through GCS like schwaber's.
//
// Everything past authentication is generated from api-reference.json (api_gen.go, one
// method per operation), plus hand-written schwaber-shaped conveniences (account.go,
// orders.go).
package ibkr

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/config"
	"github.com/darianmavgo/ibkr_api/pkg/oauth1"
	"github.com/darianmavgo/schwaber/pkg/orderintent"
	"github.com/darianmavgo/schwaber/pkg/tokensync"
)

var (
	// BaseURL is IBKR's production API. In gateway mode the gateway URL replaces it.
	BaseURL = "https://api.ibkr.com"
	// GatewayURL is the Client Portal Gateway's default address.
	GatewayURL = "https://localhost:5000"
)

// AuthMode says how the Client authenticates.
type AuthMode int

const (
	ModeGateway AuthMode = iota // browser-logged-in Client Portal Gateway session
	ModeOAuth                   // OAuth 2.0 private_key_jwt / SSO sessions, bearer token
	ModeOAuth1                  // OAuth 1.0a: RSA-signed live session token, HMAC-SHA256 per request
)

func (m AuthMode) String() string {
	switch m {
	case ModeOAuth:
		return "oauth"
	case ModeOAuth1:
		return "oauth1"
	}
	return "gateway"
}

// TokenStore is schwab.TokenStore's shape. IBKR has no rotating refresh token, so
// RefreshToken is normally empty and "refresh" means asking for a new access token.
// AccessTokenIssued is the freshness marker the GCS sync compares. (OAuth mode only.)
type TokenStore struct {
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token"`
	TokenType          string    `json:"token_type"`
	ExpiresIn          int       `json:"expires_in"`
	AccessTokenIssued  time.Time `json:"access_token_issued"`
	RefreshTokenIssued time.Time `json:"refresh_token_issued"`
}

type Option func(*Client)

func WithTokenPath(path string) Option     { return func(c *Client) { c.tokenPath = path } }
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }
func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }
func WithTokenURL(u string) Option         { return func(c *Client) { c.tokenURL = u } }
func WithKeyID(kid string) Option          { return func(c *Client) { c.keyID = kid } }
func WithScope(s string) Option            { return func(c *Client) { c.scope = s } }
func WithClientSecret(s string) Option     { return func(c *Client) { c.clientSecret = s } }

// WithCredential switches OAuth mode to SSO sessions: the client asks
// /gw/api/v1/sso-sessions for a session of IB user credential from caller ip.
func WithCredential(credential, ip string) Option {
	return func(c *Client) { c.credential, c.ip = credential, ip }
}

// WithPrivateKey sets the RSA key (PEM) that signs the client assertion.
func WithPrivateKey(pemText string) Option {
	return func(c *Client) {
		k, err := ParsePrivateKey(pemText)
		if err != nil {
			c.keyErr = err
			return
		}
		c.key = k
	}
}

// WithAutoConfirm answers IBKR's order reply prompts with confirmed=true. Off by
// default: the prompts are warnings (price far from market, size over a limit) and a
// caller must decide to accept them.
func WithAutoConfirm(on bool) Option { return func(c *Client) { c.autoConfirm = on } }

// WithOAuth1 selects OAuth 1.0a with keys (see package oauth1). It wins over OAuth 2.0
// and the gateway.
func WithOAuth1(keys *oauth1.Keys) Option { return func(c *Client) { c.oauth1Keys = keys } }

// WithGateway forces gateway mode even when a client id is configured.
func WithGateway() Option { return func(c *Client) { c.forceGateway = true } }

// WithIntentStore attaches a durable order-intent store used by PlaceOrder.
func WithIntentStore(store *orderintent.Store) Option {
	return func(c *Client) { c.intentStore = store }
}

type Client struct {
	mode         AuthMode
	forceGateway bool
	baseURL      string
	tokenURL     string
	clientID     string
	clientSecret string
	keyID        string
	key          *rsa.PrivateKey
	keyErr       error
	scope        string
	credential   string
	ip           string
	tokenPath    string
	httpClient   *http.Client
	token        *TokenStore
	tokenMu      sync.Mutex // serializes refresh + token file IO
	intentStore  *orderintent.Store
	autoConfirm  bool
	oauth1Keys   *oauth1.Keys
	oauth1       *oauth1.Session

	conidMu sync.Mutex
	conids  map[string]int64 // symbol -> IB contract id
}

// NewClient mirrors schwab.NewClient(ctx, appKey, appSecret, opts...). With an empty
// clientID it is a Client Portal Gateway client (base URL GatewayURL); with one it is an
// OAuth client (base URL BaseURL). A stored token is loaded if it exists; a missing one
// is not an error, so the caller can authenticate.
func NewClient(ctx context.Context, clientID, clientSecret string, opts ...Option) (*Client, error) {
	c := &Client{clientID: clientID, clientSecret: clientSecret, conids: map[string]int64{}}
	for _, opt := range opts {
		opt(c)
	}
	if c.keyErr != nil {
		return nil, c.keyErr
	}
	c.mode = ModeOAuth
	if clientID == "" || c.forceGateway {
		c.mode = ModeGateway
	}
	if c.oauth1Keys != nil {
		c.mode = ModeOAuth1
	}
	if c.baseURL == "" && c.mode == ModeOAuth1 {
		c.baseURL = BaseURL
	}
	if c.baseURL == "" && c.mode == ModeGateway {
		c.baseURL = GatewayURL
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 30 * time.Second, Transport: c.defaultTransport()}
	}
	if c.mode == ModeGateway && c.httpClient.Jar == nil {
		// The gateway keeps the login in a session cookie.
		if jar, err := cookiejar.New(nil); err == nil {
			c.httpClient.Jar = jar
		}
	}
	if c.mode == ModeOAuth {
		_ = c.loadToken()
	}
	if c.mode == ModeOAuth1 {
		c.oauth1 = &oauth1.Session{Keys: c.oauth1Keys, BaseURL: c.BaseURL(), HTTP: c.httpClient}
		c.restoreOAuth1Token()
	}
	return c, nil
}

// defaultTransport trusts the gateway's self-signed certificate, and only when the
// gateway is on this machine (loopback). Anything else verifies certificates normally.
func (c *Client) defaultTransport() http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if c.mode == ModeGateway && IsLoopbackURL(c.BaseURL()) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402: loopback gateway, self-signed by IBKR
	}
	return tr
}

// IsLoopbackURL reports whether u points at this machine (localhost, 127.0.0.0/8, ::1).
func IsLoopbackURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewFromConfig builds the Client the way schwaber's trader.NewTraderClient does from its
// config, minus the safety guard and intent store (see package trader).
func NewFromConfig(ctx context.Context, cfg *config.Config, opts ...Option) (*Client, error) {
	base := []Option{WithTokenPath(cfg.TokenPath), WithAutoConfirm(cfg.AutoConfirm), WithScope(cfg.Scope), WithKeyID(cfg.KeyID)}
	if cfg.Credential != "" {
		base = append(base, WithCredential(cfg.Credential, cfg.IP))
	}
	pemText := cfg.PrivateKey
	if pemText == "" && cfg.PrivateKeyAt != "" {
		data, err := os.ReadFile(cfg.PrivateKeyAt)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", config.EnvPrivateKeyAt, err)
		}
		pemText = string(data)
	}
	if pemText != "" {
		base = append(base, WithPrivateKey(pemText))
	}
	if cfg.OAuth1ConsumerKey != "" {
		keys, err := oauth1KeysFromConfig(cfg)
		if err != nil {
			return nil, err
		}
		base = append(base, WithOAuth1(keys))
	}
	switch {
	case cfg.BaseURL != "":
		base = append(base, WithBaseURL(cfg.BaseURL))
	case cfg.ClientID == "" && cfg.OAuth1ConsumerKey == "":
		base = append(base, WithBaseURL(cfg.GatewayURL))
	}
	return NewClient(ctx, cfg.ClientID, cfg.ClientSecret, append(base, opts...)...)
}

// Mode reports how this Client authenticates.
func (c *Client) Mode() AuthMode { return c.mode }

func (c *Client) BaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return BaseURL
}

func (c *Client) TokenURL() string {
	if c.tokenURL != "" {
		return c.tokenURL
	}
	return c.BaseURL() + "/oauth2/api/v1/token"
}

// Token returns the stored OAuth token (nil in gateway mode or before authenticating).
func (c *Client) Token() *TokenStore { return c.token }

// LoginURL is where a gateway session is started: open it in a browser and log in.
func (c *Client) LoginURL() string { return c.BaseURL() }

// ---- errors, exactly schwaber's set ------------------------------------------------

// ErrReauthRequired means credentials are dead or absent and a person must act: log in
// at the gateway (`ibkr auth`), or fix the OAuth client id / key.
var ErrReauthRequired = errors.New("ibkr re-authentication required")

// IsReauthRequired reports whether err (or any wrapped cause) means the user must re-auth.
func IsReauthRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrReauthRequired) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_client") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "not authenticated") ||
		strings.Contains(msg, "re-authentication required")
}

// IsPermanentAuthError reports errors only interactive re-auth can fix (a superset of
// IsReauthRequired, as in schwaber).
func IsPermanentAuthError(err error) bool {
	if err == nil {
		return false
	}
	if IsReauthRequired(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "re-authenticate") || strings.Contains(msg, "no token available") || strings.Contains(msg, "token refresh failed")
}

// ---- token file (OAuth mode) -------------------------------------------------------

func tokenObject() string {
	env := strings.TrimSpace(os.Getenv("IBKR_TOKEN_ENV"))
	if env == "" {
		env = "shared"
	}
	return "ibkr-token/" + env + ".json"
}

func readIssued(data []byte) (time.Time, error) {
	var v struct {
		Issued time.Time `json:"access_token_issued"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return time.Time{}, err
	}
	return v.Issued, nil
}

func (c *Client) loadToken() error {
	if c.tokenPath == "" {
		return fmt.Errorf("token path is empty")
	}
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return err
	}
	c.token = &TokenStore{}
	return json.Unmarshal(data, c.token)
}

func (c *Client) saveToken() error {
	if c.tokenPath == "" || c.token == nil {
		return nil
	}
	data, err := json.MarshalIndent(c.token, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.tokenPath, data, 0o600); err != nil {
		return err
	}
	// Publish so other environments follow the same token. Logged, never fatal.
	if bucket := strings.TrimSpace(os.Getenv("GCS_BUCKET")); bucket != "" {
		if err := tokensync.PushObject(context.Background(), c.tokenPath, tokenObject()); err != nil {
			log.Printf("[ibkr] WARN: failed to publish %s to gs://%s/%s: %v", c.tokenPath, bucket, tokenObject(), err)
		}
	}
	return nil
}

// ReloadTokenFromDisk re-reads the token file into memory.
func (c *Client) ReloadTokenFromDisk() error { return c.loadToken() }

func (c *Client) accessTokenValid() bool {
	if c.token == nil || c.token.AccessToken == "" || c.token.AccessTokenIssued.IsZero() {
		return false
	}
	lifetime := time.Duration(c.token.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 10 * time.Minute // IBKR gives no expiry for SSO sessions: re-ask early
	}
	skew := 2 * time.Minute
	if lifetime > 10*time.Minute && skew > lifetime/6 {
		skew = lifetime / 6
	}
	return time.Since(c.token.AccessTokenIssued) < lifetime-skew
}

func (c *Client) withTokenFileLock(fn func() error) error {
	if c.tokenPath == "" {
		return fn()
	}
	f, err := os.OpenFile(c.tokenPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fn()
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fn()
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// EnsureFreshToken makes sure the Client can send requests. OAuth mode: refresh the
// access token when needed (safe for concurrent callers). Gateway mode: confirm the
// browser-logged-in session is alive and keep it alive (tickle).
func (c *Client) EnsureFreshToken(ctx context.Context) error {
	if c.mode == ModeGateway {
		return c.EnsureSession(ctx)
	}
	if c.mode == ModeOAuth1 {
		return c.EnsureOAuth1(ctx)
	}
	return c.ensureFreshToken(ctx)
}

func (c *Client) ensureFreshToken(ctx context.Context) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	return c.withTokenFileLock(func() error {
		if c.tokenPath != "" {
			_ = c.loadToken() // another process may have refreshed it
		}
		if c.tokenPath != "" && !c.accessTokenValid() {
			if err := tokensync.PullIfNewer(ctx, c.tokenPath, tokenObject(), readIssued); err != nil {
				log.Printf("[ibkr] gcs token pull: %v (continuing with local token)", err)
			} else {
				_ = c.loadToken()
			}
		}
		if c.accessTokenValid() {
			return nil
		}
		tok, err := c.requestToken(ctx)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		tok.AccessTokenIssued = now
		if tok.RefreshToken != "" {
			tok.RefreshTokenIssued = now
		}
		c.token = tok
		if err := c.saveToken(); err != nil {
			return fmt.Errorf("token obtained but failed to persist: %w", err)
		}
		return nil
	})
}

// requestToken asks IBKR for a new access token: through an SSO session when a
// credential is configured, else from the OAuth 2.0 token endpoint.
func (c *Client) requestToken(ctx context.Context) (*TokenStore, error) {
	if c.clientID == "" {
		return nil, fmt.Errorf("%w: no client id", ErrReauthRequired)
	}
	now := time.Now().UTC()
	if c.credential != "" {
		return c.requestSSOSession(ctx, now)
	}
	body := map[string]any{"clientId": c.clientID, "scope": c.scope}
	switch {
	case c.key != nil:
		as, err := c.assertion(c.TokenURL(), now)
		if err != nil {
			return nil, err
		}
		body["clientAuthenticationMethod"] = "private_key_jwt"
		body["clientAssertion"] = as
		body["clientAssertionType"] = clientAssertionType
	case c.clientSecret != "":
		body["clientAuthenticationMethod"] = "client_secret_post"
		body["clientSecret"] = c.clientSecret
	default:
		return nil, fmt.Errorf("%w: neither a private key nor a client secret is configured", ErrReauthRequired)
	}
	var tok TokenStore
	if err := c.send(ctx, http.MethodPost, c.TokenURL(), body, &tok, authNone, nil); err != nil {
		if IsReauthRequired(err) || isStatus(err, http.StatusUnauthorized, http.StatusBadRequest) {
			return nil, fmt.Errorf("%w: token request failed: %v", ErrReauthRequired, err)
		}
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token refresh failed: empty access_token")
	}
	return &tok, nil
}

func (c *Client) requestSSOSession(ctx context.Context, now time.Time) (*TokenStore, error) {
	jwt, err := c.assertion(c.BaseURL(), now)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Active      bool   `json:"active"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	req := map[string]any{"credential": c.credential, "ip": c.ip}
	hdr := http.Header{"Authorization": {"Bearer " + jwt}}
	if err := c.send(ctx, http.MethodPost, c.BaseURL()+"/gw/api/v1/sso-sessions", req, &resp, authNone, hdr); err != nil {
		if isStatus(err, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest) {
			return nil, fmt.Errorf("%w: sso-session request failed: %v", ErrReauthRequired, err)
		}
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	if resp.AccessToken == "" {
		return nil, fmt.Errorf("token refresh failed: sso-session returned no access_token")
	}
	return &TokenStore{AccessToken: resp.AccessToken, TokenType: resp.TokenType}, nil
}

func isStatus(err error, codes ...int) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	for _, c := range codes {
		if ae.Status == c {
			return true
		}
	}
	return false
}

// ---- requests ----------------------------------------------------------------------

// authKind says which credential a request carries.
type authKind int

const (
	authNone   authKind = iota // no Authorization (token/sso requests, gateway mode)
	authSSO                    // ssoBearer: the stored access token (trading endpoints)
	authOAuth2                 // oauth2Bearer: a freshly signed JWT (account-management endpoints)
)

func (c *Client) authorize(ctx context.Context, req *http.Request, kind authKind) error {
	req.Header.Set("Accept", "application/json")
	// The Client Portal API refuses requests that carry no User-Agent.
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "ibkr_api-go/1.0")
	}
	switch {
	case kind == authNone || c.mode == ModeGateway:
		return nil
	case c.mode == ModeOAuth1:
		if kind == authOAuth2 {
			return fmt.Errorf("this endpoint needs OAuth 2.0 (oauth2Bearer); OAuth 1.0a only covers the trading endpoints")
		}
		hdr, err := c.oauth1.Authorization(ctx, req.Method, req.URL.String())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrReauthRequired, err)
		}
		req.Header.Set("Authorization", hdr)
		c.saveOAuth1Token()
	case kind == authOAuth2:
		jwt, err := c.assertion(c.BaseURL(), time.Now().UTC())
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
	default:
		if err := c.ensureFreshToken(ctx); err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	}
	return nil
}

// send is doRequest: one request with retry-once-on-401 (re-auth in OAuth mode), decoding
// a 2xx JSON body into out (which may be nil), any other status into *APIError.
func (c *Client) send(ctx context.Context, method, urlStr string, body, out any, kind authKind, extra http.Header) error {
	_, err := c.sendRaw(ctx, method, urlStr, body, out, kind, extra)
	return err
}

func (c *Client) sendRaw(ctx context.Context, method, urlStr string, body, out any, kind authKind, extra http.Header) (http.Header, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	do := func() (*http.Response, error) {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, urlStr, rd)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, vs := range extra {
			for _, v := range vs {
				req.Header.Set(k, v)
			}
		}
		if err := c.authorize(ctx, req, kind); err != nil {
			return nil, err
		}
		return c.httpClient.Do(req)
	}
	resp, err := do()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.mode == ModeOAuth && kind == authSSO {
		resp.Body.Close()
		c.tokenMu.Lock()
		if c.token != nil {
			c.token.AccessTokenIssued = time.Time{} // force a new token
		}
		c.tokenMu.Unlock()
		if resp, err = do(); err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusUnauthorized && c.mode == ModeGateway {
			return resp.Header, fmt.Errorf("%w: gateway session not authenticated (log in at %s): %w", ErrReauthRequired, c.LoginURL(), newAPIError(method, urlStr, resp, data, string(payload)))
		}
		return resp.Header, newAPIError(method, urlStr, resp, data, string(payload))
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], data...)
		} else if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("decoding %s %s: %w (body: %.200s)", method, urlStr, err, data)
		}
	}
	return resp.Header, nil
}

// Get executes a raw GET against any IBKR endpoint (a full URL), returning the body and
// status, like schwab.Client.Get.
func (c *Client) Get(ctx context.Context, endpointURL string) ([]byte, int, error) {
	var raw json.RawMessage
	hdr, err := c.sendRaw(ctx, http.MethodGet, endpointURL, nil, &raw, authSSO, nil)
	_ = hdr
	var ae *APIError
	if errors.As(err, &ae) {
		return []byte(ae.Body), ae.Status, err
	}
	if err != nil {
		return nil, 0, err
	}
	return raw, http.StatusOK, nil
}

// call is what the generated operations use: a request to path (with query) that returns
// the raw JSON body.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body any, headers http.Header, kind authKind) (json.RawMessage, error) {
	u := c.BaseURL() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var raw json.RawMessage
	if _, err := c.sendRaw(ctx, method, u, body, &raw, kind, headers); err != nil {
		return nil, err
	}
	return raw, nil
}

// ---- gateway session ---------------------------------------------------------------

// AuthStatus is the answer of POST /iserver/auth/status.
type AuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	Connected     bool   `json:"connected"`
	Competing     bool   `json:"competing"`
	Established   bool   `json:"established"`
	Message       string `json:"message"`
}

// AuthStatus asks the gateway whether the brokerage session is logged in.
func (c *Client) AuthStatus(ctx context.Context) (*AuthStatus, error) {
	var st AuthStatus
	if err := c.send(ctx, http.MethodPost, c.BaseURL()+"/v1/api/iserver/auth/status", nil, &st, authSSO, nil); err != nil {
		return nil, err
	}
	return &st, nil
}

// Tickle keeps the session alive (IBKR drops an idle session after about six minutes).
func (c *Client) Tickle(ctx context.Context) error {
	return c.send(ctx, http.MethodPost, c.BaseURL()+"/v1/api/tickle", nil, nil, authSSO, nil)
}

// EnsureSession checks the session is authenticated and tickles it. A session that is
// not authenticated is ErrReauthRequired: the fix is to log in at LoginURL.
func (c *Client) EnsureSession(ctx context.Context) error {
	st, err := c.AuthStatus(ctx)
	if err != nil {
		return err
	}
	if !st.Authenticated {
		return fmt.Errorf("%w: not authenticated (log in at %s)", ErrReauthRequired, c.LoginURL())
	}
	_ = c.Tickle(ctx)
	return nil
}

package schwab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/orderintent"
	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
)

var (
	BaseURL  = "https://api.schwabapi.com/trader/v1"
	AuthURL  = "https://api.schwabapi.com/v1/oauth/authorize"
	TokenURL = "https://api.schwabapi.com/v1/oauth/token"
)

type TokenStore struct {
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token"`
	TokenType          string    `json:"token_type"`
	ExpiresIn          int       `json:"expires_in"`
	AccessTokenIssued  time.Time `json:"access_token_issued"`
	RefreshTokenIssued time.Time `json:"refresh_token_issued"`
}

type Option func(*Client)

func WithTokenPath(path string) Option {
	return func(c *Client) {
		c.tokenPath = path
	}
}

func WithCallbackURL(url string) Option {
	return func(c *Client) {
		c.callbackURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.httpClient = client
	}
}

func WithBaseURL(u string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(u, "/")
	}
}

func WithAuthURL(u string) Option {
	return func(c *Client) {
		c.authURL = u
	}
}

func WithTokenURL(u string) Option {
	return func(c *Client) {
		c.tokenURL = u
	}
}

// WithIntentStore attaches a durable order-intent store used by PlaceOrder.
func WithIntentStore(store *orderintent.Store) Option {
	return func(c *Client) {
		c.intentStore = store
	}
}

type Client struct {
	baseURL     string
	authURL     string
	tokenURL    string
	appKey      string
	appSecret   string
	tokenPath   string
	callbackURL string
	httpClient  *http.Client
	token       *TokenStore
	tokenMu     sync.Mutex // serializes refresh + token file IO
	intentStore *orderintent.Store
}

func (c *Client) BaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return BaseURL
}

func (c *Client) AuthURL() string {
	if c.authURL != "" {
		return c.authURL
	}
	return AuthURL
}

func (c *Client) TokenURL() string {
	if c.tokenURL != "" {
		return c.tokenURL
	}
	return TokenURL
}

func (c *Client) Token() *TokenStore {
	return c.token
}

func (c *Client) AuthorizeURL() string {
	q := url.Values{}
	q.Set("client_id", c.appKey)
	q.Set("redirect_uri", c.callbackURL)
	return fmt.Sprintf("%s?%s", c.AuthURL(), q.Encode())
}

// ExtractCodeFromURL extracts the authorization code from a callback URL or raw string.
func ExtractCodeFromURL(rawInput string) (string, error) {
	rawInput = strings.TrimSpace(rawInput)
	if rawInput == "" {
		return "", fmt.Errorf("authorization input is empty")
	}

	if strings.Contains(rawInput, "code=") {
		u, err := url.Parse(rawInput)
		if err == nil && u.Query().Get("code") != "" {
			return u.Query().Get("code"), nil
		}
		idx := strings.Index(rawInput, "code=")
		sub := rawInput[idx+5:]
		if end := strings.IndexAny(sub, " &#"); end != -1 {
			sub = sub[:end]
		}
		decoded, err := url.QueryUnescape(sub)
		if err == nil && decoded != "" {
			return decoded, nil
		}
		return sub, nil
	}

	if strings.Contains(rawInput, "%") {
		if decoded, err := url.QueryUnescape(rawInput); err == nil && decoded != "" {
			return decoded, nil
		}
	}

	return rawInput, nil
}

// ExchangeAuthCode exchanges an OAuth authorization code for access and refresh tokens.
func (c *Client) ExchangeAuthCode(ctx context.Context, code string) (*TokenStore, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("authorization code is empty")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.callbackURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	basicAuth := base64.StdEncoding.EncodeToString([]byte(c.appKey + ":" + c.appSecret))
	req.Header.Set("Authorization", "Basic "+basicAuth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token exchange response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var newTok TokenStore
	if err := json.Unmarshal(body, &newTok); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w (raw response: %s)", err, string(body))
	}

	now := time.Now().UTC()
	newTok.AccessTokenIssued = now
	newTok.RefreshTokenIssued = now

	c.token = &newTok
	if err := c.saveToken(); err != nil {
		return nil, fmt.Errorf("failed to save token to %s: %w", c.tokenPath, err)
	}

	return &newTok, nil
}

func NewClient(ctx context.Context, appKey, appSecret string, opts ...Option) (*Client, error) {
	c := &Client{
		appKey:     appKey,
		appSecret:  appSecret,
		httpClient: http.DefaultClient,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Attempt to load token if file exists. If it does not exist yet, we still return the client
	// so the caller can complete the initial auth flow.
	_ = c.loadToken()

	return c, nil
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
	if err := os.WriteFile(c.tokenPath, data, 0600); err != nil {
		return err
	}
	// Push rotated refresh token to GCS so other environments aren't
	// orphaned. Failure is logged only — never fail the API call.
	tokensync.SyncAfterSave(c.tokenPath)
	return nil
}

// ReloadTokenFromDisk re-reads tokenPath into the in-memory token (e.g. after
// GAE re-fetches schwab-token from Secret Manager).
func (c *Client) ReloadTokenFromDisk() error {
	return c.loadToken()
}

// ErrReauthRequired means the stored refresh token is dead (revoked/expired/invalid).
// Callers should run interactive OAuth (`schwaber auth` / GAE /auth/schwab) — retrying refresh will not help.
var ErrReauthRequired = errors.New("schwab re-authentication required")

// IsReauthRequired reports whether err (or any wrapped cause) means the user must re-auth.
func IsReauthRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrReauthRequired) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "unsupported_token_type") ||
		strings.Contains(msg, "refresh token is invalid") ||
		strings.Contains(msg, "expired or revoked") ||
		strings.Contains(msg, "re-authentication required")
}

// IsPermanentAuthError reports errors that mean the stored refresh token is
// dead and only interactive re-auth (`schwaber auth`) can fix it: everything
// IsReauthRequired matches, plus the wrapped "expired/no token/refresh failed"
// messages callers see after ensureFreshToken has already retried.
func IsPermanentAuthError(err error) bool {
	if err == nil {
		return false
	}
	if IsReauthRequired(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "authentication expired or invalid") ||
		strings.Contains(msg, "re-authenticate") ||
		strings.Contains(msg, "no token available") ||
		strings.Contains(msg, "token refresh failed")
}

// EnsureFreshToken refreshes the access token when needed. Safe for concurrent callers.
func (c *Client) EnsureFreshToken(ctx context.Context) error {
	return c.ensureFreshToken(ctx)
}

func (c *Client) accessTokenValid() bool {
	if c.token == nil || c.token.AccessToken == "" || c.token.AccessTokenIssued.IsZero() {
		return false
	}
	lifetime := time.Duration(c.token.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 30 * time.Minute
	}
	// Refresh 2 minutes before Schwab expiry (or at 25m for the classic 30m token).
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
	lockPath := c.tokenPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		// Two processes refreshing at once can each spend the one-time refresh token and
		// invalidate the other's, so an unlockable token file stops the refresh.
		return fmt.Errorf("cannot open the token lock file %s: %w (check that the folder holding the token is writable)", lockPath, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("cannot lock %s: %w (another process may hold the token; retry, or check the filesystem supports flock)", lockPath, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (c *Client) ensureFreshToken(ctx context.Context) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	return c.withTokenFileLock(func() error {
		// Another process may have rotated the refresh token — always
		// reload from local disk first (cheap; happens on every call).
		loadErr := error(nil)
		if c.tokenPath != "" {
			loadErr = c.loadToken()
		}

		// No usable local token yet (e.g. a fresh GAE instance: /tmp is
		// ephemeral, wiped every deploy/restart) OR the local access token
		// is stale — either way, try GCS before giving up or refreshing.
		// SyncBeforeUse only overwrites local when the remote copy is
		// strictly newer, so this never regresses a fresher local token.
		if c.tokenPath != "" && (c.token == nil || !c.accessTokenValid()) {
			if err := tokensync.SyncBeforeUse(ctx, c.tokenPath); err != nil {
				log.Printf("[schwab] gcs token pull: %v (continuing with local token)", err)
			} else {
				loadErr = c.loadToken()
			}
		}

		if c.token == nil {
			return fmt.Errorf("%w: no token available (%v); run `schwaber auth`", ErrReauthRequired, loadErr)
		}
		if c.accessTokenValid() {
			return nil
		}
		if c.token.RefreshToken == "" {
			return fmt.Errorf("%w: no refresh_token; run `schwaber auth`", ErrReauthRequired)
		}

		form := url.Values{}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", c.token.RefreshToken)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL(), strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		basicAuth := base64.StdEncoding.EncodeToString([]byte(c.appKey + ":" + c.appSecret))
		req.Header.Set("Authorization", "Basic "+basicAuth)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("token refresh request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(body))
			if IsReauthRequired(err) {
				return fmt.Errorf("%w: %s", ErrReauthRequired, string(body))
			}
			return err
		}

		var newTok TokenStore
		if err := json.Unmarshal(body, &newTok); err != nil {
			return err
		}
		if newTok.AccessToken == "" {
			return fmt.Errorf("token refresh returned empty access_token")
		}

		now := time.Now().UTC()
		c.token.AccessToken = newTok.AccessToken
		c.token.AccessTokenIssued = now
		c.token.TokenType = newTok.TokenType
		if newTok.ExpiresIn > 0 {
			c.token.ExpiresIn = newTok.ExpiresIn
		}
		if newTok.RefreshToken != "" {
			c.token.RefreshToken = newTok.RefreshToken
			c.token.RefreshTokenIssued = now
		}
		if err := c.saveToken(); err != nil {
			return fmt.Errorf("token refreshed but failed to persist: %w", err)
		}
		return nil
	})
}

// Get executes a raw GET request against any Charles Schwab API endpoint, automatically refreshing the token.
func (c *Client) Get(ctx context.Context, endpointURL string) ([]byte, int, error) {
	if err := c.ensureFreshToken(ctx); err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.doGetWithRetry(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	// If 401 Unauthorized, access token might have been invalidated early on server. Try refresh once.
	if resp.StatusCode == http.StatusUnauthorized && c.token != nil && c.token.RefreshToken != "" {
		c.token.AccessTokenIssued = time.Time{}
		if refErr := c.ensureFreshToken(ctx); refErr == nil {
			retryReq, rErr := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
			if rErr == nil {
				retryReq.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
				retryReq.Header.Set("Accept", "application/json")
				retryResp, doErr := c.httpClient.Do(retryReq)
				if doErr == nil {
					defer retryResp.Body.Close()
					retryBody, readErr := io.ReadAll(retryResp.Body)
					if readErr == nil {
						if retryResp.StatusCode >= 400 {
							return retryBody, retryResp.StatusCode, fmt.Errorf("API error (HTTP %d): %s", retryResp.StatusCode, string(retryBody))
						}
						return retryBody, retryResp.StatusCode, nil
					}
				}
			}
		}
	}

	if resp.StatusCode >= 400 {
		return body, resp.StatusCode, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return body, resp.StatusCode, nil
}

func (c *Client) doRequest(ctx context.Context, method, urlStr string, body interface{}, out interface{}) error {
	if err := c.ensureFreshToken(ctx); err != nil {
		return err
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	var resp *http.Response
	if method == http.MethodGet {
		resp, err = c.doGetWithRetry(ctx, req)
	} else {
		resp, err = c.httpClient.Do(req)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && c.token != nil && c.token.RefreshToken != "" {
		c.token.AccessTokenIssued = time.Time{}
		if refErr := c.ensureFreshToken(ctx); refErr == nil {
			var retryReqBody io.Reader
			if body != nil {
				retryData, _ := json.Marshal(body)
				retryReqBody = bytes.NewReader(retryData)
			}
			retryReq, rErr := http.NewRequestWithContext(ctx, method, urlStr, retryReqBody)
			if rErr == nil {
				retryReq.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
				retryReq.Header.Set("Accept", "application/json")
				if body != nil {
					retryReq.Header.Set("Content-Type", "application/json")
				}
				retryResp, doErr := c.httpClient.Do(retryReq)
				if doErr == nil {
					defer retryResp.Body.Close()
					if retryResp.StatusCode < 400 {
						if out != nil {
							return json.NewDecoder(retryResp.Body).Decode(out)
						}
						return nil
					}
					bodyBytes, _ := io.ReadAll(retryResp.Body)
					return newAPIError(method, urlStr, retryResp, bodyBytes, "")
				}
			}
		}
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return newAPIError(method, urlStr, resp, bodyBytes, "")
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) ListLinkedAccounts(ctx context.Context) ([]LinkedAccount, error) {
	var accounts []LinkedAccount
	urlStr := fmt.Sprintf("%s/accounts/accountNumbers", c.BaseURL())
	err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &accounts)
	return accounts, err
}

func (c *Client) GetAccount(ctx context.Context, hashValue, fields string) (*Account, error) {
	var account Account
	urlStr := fmt.Sprintf("%s/accounts/%s", c.BaseURL(), hashValue)
	if fields != "" {
		urlStr += "?fields=" + url.QueryEscape(fields)
	}
	err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &account)
	return &account, err
}

func (c *Client) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	marketDataBaseURL := "https://api.schwabapi.com/marketdata/v1"
	urlStr := fmt.Sprintf("%s/quotes?symbols=%s", marketDataBaseURL, url.QueryEscape(symbol))

	var response map[string]Quote
	err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &response)
	if err != nil {
		return nil, err
	}

	if q, ok := response[symbol]; ok {
		return &q, nil
	}
	return nil, fmt.Errorf("quote not found for symbol: %s", symbol)
}

// GetQuotes fetches quotes for several symbols in one request. A symbol Schwab
// cannot quote is simply absent from the result, as with the errors object it
// returns beside the quotes.
func (c *Client) GetQuotes(ctx context.Context, symbols []string) (map[string]Quote, error) {
	if len(symbols) == 0 {
		return map[string]Quote{}, nil
	}
	marketDataBaseURL := "https://api.schwabapi.com/marketdata/v1"
	urlStr := fmt.Sprintf("%s/quotes?symbols=%s", marketDataBaseURL, url.QueryEscape(strings.Join(symbols, ",")))

	var raw map[string]json.RawMessage
	if err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]Quote, len(raw))
	for sym, msg := range raw {
		var q Quote
		if json.Unmarshal(msg, &q) != nil || len(q.Quote) == 0 {
			continue // the errors object, or a symbol with no quote
		}
		out[sym] = q
	}
	return out, nil
}

// placeOrderHTTPRaw POSTs the order JSON with no intent bookkeeping.
func (c *Client) placeOrderHTTPRaw(ctx context.Context, hashValue string, order *Order) (string, error) {
	urlStr := fmt.Sprintf("%s/accounts/%s/orders", c.BaseURL(), hashValue)

	if err := c.ensureFreshToken(ctx); err != nil {
		return "", err
	}

	data, err := json.Marshal(order)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", newAPIError(http.MethodPost, urlStr, resp, bodyBytes, string(data))
	}

	location := resp.Header.Get("Location")
	return location, nil
}

// IntentStore returns the attached order-intent store (may be nil).
func (c *Client) IntentStore() *orderintent.Store {
	if c == nil {
		return nil
	}
	return c.intentStore
}

// EnsureIntentStore opens the default intents DB (Turso)
// when none is attached. Safe to call multiple times.
func (c *Client) EnsureIntentStore() error {
	if c == nil {
		return fmt.Errorf("nil client")
	}
	if c.intentStore != nil {
		return nil
	}
	store, err := orderintent.OpenDefault(c.tokenPath)
	if err != nil {
		return err
	}
	c.intentStore = store
	return nil
}

func (c *Client) ListOrders(ctx context.Context, req OrderListRequest) ([]Order, error) {
	var orders []Order

	query := url.Values{}
	if !req.From.IsZero() {
		query.Add("fromEnteredTime", req.From.Format(time.RFC3339))
	}
	if !req.To.IsZero() {
		query.Add("toEnteredTime", req.To.Format(time.RFC3339))
	}
	if req.Status != "" {
		query.Add("status", req.Status)
	}

	urlStr := fmt.Sprintf("%s/orders?%s", c.BaseURL(), query.Encode())
	err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &orders)
	return orders, err
}

func (c *Client) CancelOrder(ctx context.Context, hashValue, orderID string) error {
	urlStr := fmt.Sprintf("%s/accounts/%s/orders/%s", c.BaseURL(), hashValue, orderID)
	return c.doRequest(ctx, http.MethodDelete, urlStr, nil, nil)
}

// ListAccountOrders returns orders for one account hash.
func (c *Client) ListAccountOrders(ctx context.Context, hashValue string, req OrderListRequest) ([]Order, error) {
	var orders []Order
	query := url.Values{}
	if !req.From.IsZero() {
		query.Add("fromEnteredTime", req.From.Format(time.RFC3339))
	}
	if !req.To.IsZero() {
		query.Add("toEnteredTime", req.To.Format(time.RFC3339))
	}
	if req.Status != "" {
		query.Add("status", req.Status)
	}
	urlStr := fmt.Sprintf("%s/accounts/%s/orders?%s", c.BaseURL(), hashValue, query.Encode())
	err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &orders)
	return orders, err
}

// GetOrder fetches a single order by id for an account.
func (c *Client) GetOrder(ctx context.Context, hashValue, orderID string) (*Order, error) {
	var order Order
	urlStr := fmt.Sprintf("%s/accounts/%s/orders/%s", c.BaseURL(), hashValue, orderID)
	if err := c.doRequest(ctx, http.MethodGet, urlStr, nil, &order); err != nil {
		return nil, err
	}
	return &order, nil
}

// PreviewOrder asks Schwab to validate order for the account without placing it
// (POST /accounts/{hash}/previewOrder) and returns Schwab's JSON answer: the
// order as Schwab understands it plus orderValidationResult (rejects, warnings,
// alerts) and commission and margin effects. Nothing is placed or recorded, so
// it is how to learn why a placeOrder was refused with a generic 400.
func (c *Client) PreviewOrder(ctx context.Context, hashValue string, order *Order) (json.RawMessage, error) {
	if order == nil {
		return nil, fmt.Errorf("nil order")
	}
	urlStr := fmt.Sprintf("%s/accounts/%s/previewOrder", c.BaseURL(), hashValue)
	var out json.RawMessage
	if err := c.doRequest(ctx, http.MethodPost, urlStr, order, &out); err != nil {
		return nil, err
	}
	return out, nil
}

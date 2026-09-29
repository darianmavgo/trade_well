package ibkr

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darianmavgo/ibkr_api/pkg/config"
	"github.com/darianmavgo/ibkr_api/pkg/oauth1"
)

// oauth1KeysFromConfig reads the three key files and the issued token from cfg.
func oauth1KeysFromConfig(cfg *config.Config) (*oauth1.Keys, error) {
	read := func(env, path string) (string, error) {
		if path == "" {
			return "", fmt.Errorf("%s is not set", env)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", env, err)
		}
		return string(b), nil
	}
	sigPEM, err := read(config.EnvOAuth1SignatureKey, cfg.OAuth1SignatureKeyPath)
	if err != nil {
		return nil, err
	}
	encPEM, err := read(config.EnvOAuth1EncryptKey, cfg.OAuth1EncryptionKeyPath)
	if err != nil {
		return nil, err
	}
	dhPEM, err := read(config.EnvOAuth1DHParam, cfg.OAuth1DHParamPath)
	if err != nil {
		return nil, err
	}
	sig, err := oauth1.ParseRSAPrivateKey(sigPEM)
	if err != nil {
		return nil, err
	}
	enc, err := oauth1.ParseRSAPrivateKey(encPEM)
	if err != nil {
		return nil, err
	}
	p, g, err := oauth1.ParseDHParams(dhPEM)
	if err != nil {
		return nil, err
	}
	if cfg.OAuth1AccessToken == "" || cfg.OAuth1TokenSecret == "" {
		return nil, fmt.Errorf("%s and %s must both be set (from IBKR's self-service portal)", config.EnvOAuth1AccessToken, config.EnvOAuth1TokenSecret)
	}
	return &oauth1.Keys{ConsumerKey: cfg.OAuth1ConsumerKey, AccessToken: cfg.OAuth1AccessToken, AccessTokenSecret: cfg.OAuth1TokenSecret,
		Signature: sig, Encryption: enc, DHPrime: p, DHGenerator: g, Realm: cfg.OAuth1Realm}, nil
}

// The live session token is kept in the token file in TokenStore's shape (access_token =
// the token, base64) so a restart, and GCS sync, reuse it instead of renegotiating.
func (c *Client) restoreOAuth1Token() {
	if c.tokenPath == "" || c.loadToken() != nil || c.token == nil || c.token.AccessToken == "" {
		return
	}
	raw, err := decodeB64(c.token.AccessToken)
	if err != nil {
		return
	}
	c.oauth1.Restore(&oauth1.SessionToken{Token: raw,
		Expires: c.token.AccessTokenIssued.Add(time.Duration(c.token.ExpiresIn) * time.Second)})
}

func (c *Client) saveOAuth1Token() {
	t := c.oauth1.Token()
	if t == nil || c.tokenPath == "" {
		return
	}
	b64 := t.Base64()
	if c.token != nil && c.token.AccessToken == b64 {
		return // unchanged
	}
	now := time.Now().UTC()
	c.token = &TokenStore{AccessToken: b64, TokenType: "oauth1-lst", AccessTokenIssued: now, RefreshTokenIssued: now,
		ExpiresIn: int(time.Until(t.Expires).Seconds())}
	if err := c.saveToken(); err != nil {
		log.Printf("[ibkr] WARN: could not persist the live session token: %v", err)
	}
}

// EnsureOAuth1 makes sure there is a live session token and a brokerage session behind
// it: it negotiates the token when needed, then, if the brokerage session is not
// authenticated, starts one (POST /iserver/auth/ssodh/init). Errors that mean the
// credentials are wrong are ErrReauthRequired.
func (c *Client) EnsureOAuth1(ctx context.Context) error {
	if _, err := c.oauth1.Ensure(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrReauthRequired, err)
	}
	c.saveOAuth1Token()
	st, err := c.AuthStatus(ctx)
	if err == nil && st.Authenticated {
		_ = c.Tickle(ctx)
		return nil
	}
	if _, err := c.TradingSessionInitializeSession(ctx, map[string]any{"publish": true, "compete": true}); err != nil {
		return fmt.Errorf("starting the brokerage session: %w", err)
	}
	return nil
}

func decodeB64(s string) ([]byte, error) {
	return base64StdDecode(strings.TrimSpace(s))
}

var _ = http.MethodGet

func base64StdDecode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

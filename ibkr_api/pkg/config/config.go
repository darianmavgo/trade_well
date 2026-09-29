// Package config is the IBKR twin of schwaber/pkg/config. Configuration is read from
// the environment only; the environment is filled once at process start from the same
// GCS object as everything else (schwaber config.Bootstrap, gs://$GCS_BUCKET/config/env),
// so there is no .env, no Secret Manager and nothing to set up per binary.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Environment variable names (IBKR_*). Documented in the README.
const (
	EnvAccountID          = "IBKR_ACCOUNT_ID"   // the account this login trades (also accepted: IBKR_ACCOUNT_IDS, comma-separated)
	EnvUsername           = "IBKR_USERNAME"     // informational; the Gateway login itself happens in the browser
	EnvTradingMode        = "IBKR_TRADING_MODE" // paper or live: refuses orders when the account id says otherwise
	EnvGatewayURL         = "IBKR_GATEWAY_URL"  // Client Portal Gateway, default https://localhost:5000
	EnvOAuth1ConsumerKey  = "IBKR_OAUTH1_CONSUMER_KEY"
	EnvOAuth1AccessToken  = "IBKR_OAUTH1_ACCESS_TOKEN"
	EnvOAuth1TokenSecret  = "IBKR_OAUTH1_ACCESS_TOKEN_SECRET" // as issued: base64, RSA-encrypted
	EnvOAuth1SignatureKey = "IBKR_OAUTH1_SIGNATURE_KEY_PATH"  // private_signature.pem
	EnvOAuth1EncryptKey   = "IBKR_OAUTH1_ENCRYPTION_KEY_PATH" // private_encryption.pem
	EnvOAuth1DHParam      = "IBKR_OAUTH1_DHPARAM_PATH"        // dhparam.pem
	EnvOAuth1Realm        = "IBKR_OAUTH1_REALM"               // default limited_poa; test_realm for TESTCONS
	EnvClientID           = "IBKR_CLIENT_ID"
	EnvKeyID              = "IBKR_KEY_ID"
	EnvPrivateKey         = "IBKR_PRIVATE_KEY"      // PEM text (RSA, PKCS#1 or PKCS#8)
	EnvPrivateKeyAt       = "IBKR_PRIVATE_KEY_PATH" // or a path to the PEM file
	EnvClientSecret       = "IBKR_CLIENT_SECRET"    // only for client_secret_basic/post
	EnvCredential         = "IBKR_CREDENTIAL"       // IB username: switches auth to SSO sessions
	EnvIP                 = "IBKR_IP"               // caller IP registered for SSO sessions
	EnvScope              = "IBKR_SCOPE"
	EnvTokenPath          = "IBKR_TOKEN_PATH"
	EnvBaseURL            = "IBKR_BASE_URL"
	EnvAccountIDs         = "IBKR_ACCOUNT_IDS"
	EnvDBPath             = "IBKR_DB_PATH"
	EnvLogFile            = "IBKR_LOG_FILE"
	EnvDebug              = "IBKR_DEBUG"
	EnvAutoConfirm        = "IBKR_AUTO_CONFIRM_REPLIES"
	EnvDryRun             = "DRY_RUN_DEFAULT" // shared with schwaber
)

// Config mirrors schwaber's config.Config field for field where an IBKR equivalent
// exists (AppKey→ClientID, CallbackURL is not needed: there is no browser redirect).
type Config struct {
	OAuth1ConsumerKey, OAuth1AccessToken, OAuth1TokenSecret                         string
	OAuth1SignatureKeyPath, OAuth1EncryptionKeyPath, OAuth1DHParamPath, OAuth1Realm string

	// Username and TradingMode come from the .env. IBKR_PASSWORD is deliberately NOT
	// loaded: nothing in the API needs it (the Gateway login is interactive), and a
	// password that is never read cannot be logged.
	Username    string
	TradingMode string // "paper" or "live" ("" = not stated)
	GatewayURL  string

	ClientID      string
	KeyID         string
	PrivateKey    string // PEM text; PrivateKeyPath is read when this is empty
	PrivateKeyAt  string
	ClientSecret  string
	Credential    string
	IP            string
	Scope         string
	TokenPath     string
	BaseURL       string
	AccountIDs    []string
	DBPath        string
	LogFile       string
	Debug         bool
	DryRunDefault bool
	// AutoConfirm answers IBKR's order "reply" prompts (price/size/risk warnings)
	// with confirmed=true. Off by default: a warning is a decision, not a formality.
	AutoConfirm bool
}

// Load builds the Config from environment variables. It reads nothing else, so it is
// free to call anywhere, tests included.
func Load() *Config {
	ids := splitList(os.Getenv(EnvAccountIDs))
	if one := strings.TrimSpace(os.Getenv(EnvAccountID)); one != "" {
		found := false
		for _, id := range ids {
			found = found || id == one
		}
		if !found {
			ids = append([]string{one}, ids...)
		}
	}
	return &Config{
		OAuth1ConsumerKey:       os.Getenv(EnvOAuth1ConsumerKey),
		OAuth1AccessToken:       os.Getenv(EnvOAuth1AccessToken),
		OAuth1TokenSecret:       os.Getenv(EnvOAuth1TokenSecret),
		OAuth1SignatureKeyPath:  os.Getenv(EnvOAuth1SignatureKey),
		OAuth1EncryptionKeyPath: os.Getenv(EnvOAuth1EncryptKey),
		OAuth1DHParamPath:       os.Getenv(EnvOAuth1DHParam),
		OAuth1Realm:             os.Getenv(EnvOAuth1Realm),
		Username:                os.Getenv(EnvUsername),
		TradingMode:             strings.ToLower(strings.Trim(strings.TrimSpace(os.Getenv(EnvTradingMode)), `"'`)),
		GatewayURL:              getEnvDefault(EnvGatewayURL, "https://localhost:5000"),
		ClientID:                os.Getenv(EnvClientID),
		KeyID:                   os.Getenv(EnvKeyID),
		PrivateKey:              os.Getenv(EnvPrivateKey),
		PrivateKeyAt:            os.Getenv(EnvPrivateKeyAt),
		ClientSecret:            os.Getenv(EnvClientSecret),
		Credential:              os.Getenv(EnvCredential),
		IP:                      os.Getenv(EnvIP),
		Scope:                   getEnvDefault(EnvScope, ""),
		TokenPath:               getEnvDefault(EnvTokenPath, "ibkr_token.json"),
		BaseURL:                 getEnvDefault(EnvBaseURL, ""),
		AccountIDs:              ids,
		DBPath:                  getEnvDefault(EnvDBPath, "ibkr.db"),
		LogFile:                 getEnvDefault(EnvLogFile, "ibkr_api.log"),
		Debug:                   getEnvBool(EnvDebug, false),
		DryRunDefault:           getEnvBool(EnvDryRun, true),
		AutoConfirm:             getEnvBool(EnvAutoConfirm, false),
	}
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes":
			return true
		}
		if _, err := strconv.ParseBool(v); err == nil {
			b, _ := strconv.ParseBool(v)
			return b
		}
		return false
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

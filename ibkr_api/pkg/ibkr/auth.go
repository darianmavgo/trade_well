package ibkr

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// clientAssertionType is the RFC 7523 value IBKR's /oauth2/api/v1/token expects with
// clientAuthenticationMethod "private_key_jwt".
const clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// ParsePrivateKey reads an RSA private key from PEM text (PKCS#1 or PKCS#8).
func ParsePrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		// A one-line config value cannot hold a multi-line PEM: accept it base64-encoded.
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pemText)); err == nil {
			block, _ = pem.Decode(raw)
		}
	}
	if block == nil {
		return nil, errors.New("ibkr: private key is neither PEM nor base64-encoded PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ibkr: private key is neither PKCS#1 nor PKCS#8: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("ibkr: private key is not RSA (RS256 needs RSA)")
	}
	return rk, nil
}

// SignJWT returns a compact RS256 JWT over claims, signed with key. kid, when set, goes
// in the header. It is the "RS256-signed JWT ... signed with your registered private
// key" that IBKR's oauth2Bearer scheme names.
func SignJWT(key *rsa.PrivateKey, kid string, claims map[string]any) (string, error) {
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// newJTI is a random unique id for a JWT.
func newJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// assertion builds the client assertion: iss = sub = client id, aud = the endpoint it is
// presented to, short-lived, single-use (jti).
func (c *Client) assertion(audience string, now time.Time) (string, error) {
	if c.key == nil {
		return "", fmt.Errorf("%w: no private key configured (IBKR_PRIVATE_KEY or IBKR_PRIVATE_KEY_PATH)", ErrReauthRequired)
	}
	return SignJWT(c.key, c.keyID, map[string]any{
		"iss": c.clientID,
		"sub": c.clientID,
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(60 * time.Second).Unix(),
		"jti": newJTI(),
	})
}

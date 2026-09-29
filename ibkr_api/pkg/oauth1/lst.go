package oauth1

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// Keys is everything IBKR's self-service portal gives or asks for.
type Keys struct {
	ConsumerKey       string
	AccessToken       string
	AccessTokenSecret string // as issued: base64, RSA-encrypted with the encryption public key
	Signature         *rsa.PrivateKey
	Encryption        *rsa.PrivateKey
	DHPrime           *big.Int // from the registered dhparam.pem
	DHGenerator       *big.Int // normally 2
	Realm             string   // default DefaultRealm
}

func (k *Keys) realm() string {
	if k.Realm != "" {
		return k.Realm
	}
	return DefaultRealm
}

// ParseRSAPrivateKey reads an RSA private key (PKCS#1 or PKCS#8 PEM).
func ParseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("oauth1: not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("oauth1: private key is neither PKCS#1 nor PKCS#8: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("oauth1: private key is not RSA")
	}
	return rk, nil
}

// ParseDHParams reads `openssl dhparam` output (PEM "DH PARAMETERS": SEQUENCE { p, g }).
func ParseDHParams(pemText string) (p, g *big.Int, err error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, nil, errors.New("oauth1: dhparam is not PEM")
	}
	var dh struct {
		P *big.Int
		G *big.Int
	}
	if _, err := asn1.Unmarshal(block.Bytes, &dh); err != nil {
		return nil, nil, fmt.Errorf("oauth1: dhparam: %w", err)
	}
	if dh.P == nil || dh.G == nil || dh.P.Sign() <= 0 {
		return nil, nil, errors.New("oauth1: dhparam has no prime/generator")
	}
	return dh.P, dh.G, nil
}

// DecryptAccessTokenSecret decrypts the access-token secret IBKR issues (base64,
// RSAES-PKCS1-v1_5 under the encryption key you registered).
func (k *Keys) DecryptAccessTokenSecret() ([]byte, error) {
	if k.Encryption == nil {
		return nil, errors.New("oauth1: no encryption private key")
	}
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.AccessTokenSecret))
	if err != nil {
		return nil, fmt.Errorf("oauth1: access token secret is not base64: %w", err)
	}
	return rsa.DecryptPKCS1v15(rand.Reader, k.Encryption, ct)
}

// ToByteArray is the two's-complement big-endian byte form IBKR's samples use (Java's
// BigInteger.toByteArray): a leading 0x00 is added when the top bit would read as a sign.
func ToByteArray(x *big.Int) []byte {
	b := x.Bytes()
	if len(b) == 0 {
		return []byte{0}
	}
	if b[0]&0x80 != 0 {
		return append([]byte{0}, b...)
	}
	return b
}

// LSTRequest is a prepared live_session_token request: send Header as Authorization on a
// POST to URL, then hand the JSON answer and A to (*Keys).LiveSessionToken.
type LSTRequest struct {
	URL    string
	Header string
	A      *big.Int // our DH secret; never sent
}

// NewLSTRequest builds the RSA-SHA256 signed request for baseURL + /v1/api/oauth/live_session_token.
func (k *Keys) NewLSTRequest(baseURL string, now time.Time) (*LSTRequest, error) {
	if k.Signature == nil || k.DHPrime == nil || k.DHGenerator == nil {
		return nil, errors.New("oauth1: signature key and DH parameters are required")
	}
	secret, err := k.DecryptAccessTokenSecret()
	if err != nil {
		return nil, err
	}
	a, err := rand.Int(rand.Reader, new(big.Int).Sub(k.DHPrime, big.NewInt(2)))
	if err != nil {
		return nil, err
	}
	a.Add(a, big.NewInt(1))
	challenge := new(big.Int).Exp(k.DHGenerator, a, k.DHPrime)

	endpoint := strings.TrimRight(baseURL, "/") + "/v1/api/oauth/live_session_token"
	params := Params{
		"oauth_consumer_key":       k.ConsumerKey,
		"oauth_nonce":              Nonce(),
		"oauth_signature_method":   MethodRSASHA256,
		"oauth_timestamp":          timestamp(now),
		"oauth_token":              k.AccessToken,
		"diffie_hellman_challenge": hex.EncodeToString(challenge.Bytes()),
	}
	base, err := BaseString("POST", endpoint, params, url.Values{}, hex.EncodeToString(secret))
	if err != nil {
		return nil, err
	}
	sig, err := SignRSASHA256(k.Signature, base)
	if err != nil {
		return nil, err
	}
	params["oauth_signature"] = sig
	return &LSTRequest{URL: endpoint, Header: Header(k.realm(), params), A: a}, nil
}

// LSTResponse is IBKR's answer to the live_session_token request.
type LSTResponse struct {
	DiffieHellmanResponse     string `json:"diffie_hellman_response"`
	LiveSessionTokenSignature string `json:"live_session_token_signature"`
	LiveSessionTokenExpiry    int64  `json:"live_session_token_expiration"` // epoch milliseconds
}

// SessionToken is the live session token: the shared secret every later request is
// HMAC-signed with.
type SessionToken struct {
	Token   []byte // raw bytes
	Expires time.Time
}

// Base64 is the token as IBKR prints it (and as it is stored).
func (t *SessionToken) Base64() string { return base64.StdEncoding.EncodeToString(t.Token) }

// LiveSessionToken finishes the exchange: K = B^a mod p, LST = HMAC-SHA1(K, secret), and
// verifies IBKR's signature of it (HMAC-SHA1(LST, consumer key)) before trusting it.
func (k *Keys) LiveSessionToken(resp LSTResponse, a *big.Int) (*SessionToken, error) {
	secret, err := k.DecryptAccessTokenSecret()
	if err != nil {
		return nil, err
	}
	b, ok := new(big.Int).SetString(strings.TrimSpace(resp.DiffieHellmanResponse), 16)
	if !ok || b.Sign() <= 0 {
		return nil, fmt.Errorf("oauth1: bad diffie_hellman_response")
	}
	shared := new(big.Int).Exp(b, a, k.DHPrime)
	lst := hmacSHA1(ToByteArray(shared), secret)

	want := strings.ToLower(strings.TrimSpace(resp.LiveSessionTokenSignature))
	got := hex.EncodeToString(hmacSHA1(lst, []byte(k.ConsumerKey)))
	if want == "" || got != want {
		return nil, errors.New("oauth1: live session token signature does not verify (wrong consumer key, secret or DH parameters)")
	}
	return &SessionToken{Token: lst, Expires: time.UnixMilli(resp.LiveSessionTokenExpiry).UTC()}, nil
}

// Sign returns the Authorization header for one API request, HMAC-SHA256 under the live
// session token. query is the request's query string values. The JSON body is not part
// of the signature (IBKR signs only the URL and OAuth parameters).
func (k *Keys) Sign(method, rawURL string, query url.Values, lst *SessionToken, now time.Time) (string, error) {
	if lst == nil || len(lst.Token) == 0 {
		return "", errors.New("oauth1: no live session token")
	}
	params := Params{
		"oauth_consumer_key":     k.ConsumerKey,
		"oauth_nonce":            Nonce(),
		"oauth_signature_method": MethodHMACSHA256,
		"oauth_timestamp":        timestamp(now),
		"oauth_token":            k.AccessToken,
	}
	base, err := BaseString(method, rawURL, params, query, "")
	if err != nil {
		return "", err
	}
	params["oauth_signature"] = SignHMACSHA256(lst.Token, base)
	return Header(k.realm(), params), nil
}

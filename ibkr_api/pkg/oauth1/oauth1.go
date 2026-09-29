// Package oauth1 implements IBKR's OAuth 1.0a for the Web API: the RSA-SHA256 signed
// request that trades a decrypted access-token secret and a Diffie-Hellman challenge for
// a live session token (LST), and the HMAC-SHA256 signing of every API request with it.
//
// It is pure: it builds and verifies headers and does the arithmetic. Sending requests is
// session.go. Nothing here has been run against IBKR (that needs your consumer key and
// the keys registered in IBKR's self-service portal); the tests check the cryptography
// against itself and the encodings against RFC 5849 / RFC 3986.
package oauth1

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Signature methods IBKR accepts.
const (
	MethodRSASHA256  = "RSA-SHA256"  // the live_session_token request
	MethodHMACSHA256 = "HMAC-SHA256" // every request after it
)

// DefaultRealm is IBKR's realm for production consumers; TESTCONS uses "test_realm".
const DefaultRealm = "limited_poa"

// Encode percent-encodes s per RFC 3986 (unreserved characters A-Za-z0-9-._~ stay).
func Encode(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// Params is an OAuth parameter set (protocol parameters plus the request's query).
type Params map[string]string

// BaseString is the RFC 5849 signature base string: METHOD & encoded-URL & encoded,
// sorted, "&"-joined name=value pairs. rawURL is used without its query (the query
// belongs in extra). prepend, when non-empty, goes in front of the whole string: IBKR's
// live_session_token request prepends the hex of the decrypted access-token secret.
func BaseString(method, rawURL string, oauth Params, extra url.Values, prepend string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.RawQuery, u.Fragment = "", ""
	base := u.String()

	type kv struct{ k, v string }
	var pairs []kv
	for k, v := range oauth {
		if k == "realm" || k == "oauth_signature" {
			continue // never signed
		}
		pairs = append(pairs, kv{Encode(k), Encode(v)})
	}
	for k, vs := range extra {
		for _, v := range vs {
			pairs = append(pairs, kv{Encode(k), Encode(v)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return prepend + strings.ToUpper(method) + "&" + Encode(base) + "&" + Encode(strings.Join(parts, "&")), nil
}

// Header renders the Authorization header value: OAuth realm="..", name="value", ...
// with every value percent-encoded and names sorted.
func Header(realm string, p Params) string {
	names := make([]string, 0, len(p))
	for k := range p {
		if k != "realm" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString(`OAuth realm="` + realm + `"`)
	for _, k := range names {
		sb.WriteString(fmt.Sprintf(`, %s="%s"`, k, Encode(p[k])))
	}
	return sb.String()
}

// SignRSASHA256 is RSASSA-PKCS1-v1_5 over SHA-256, base64-encoded.
func SignRSASHA256(key *rsa.PrivateKey, message string) (string, error) {
	sum := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// SignHMACSHA256 is HMAC-SHA256 of message under key, base64-encoded.
func SignHMACSHA256(key []byte, message string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// hmacSHA1 is used only to derive and check the live session token, as IBKR specifies.
func hmacSHA1(key, msg []byte) []byte {
	m := hmac.New(sha1.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// Nonce returns a random 16-hex-character oauth_nonce.
func Nonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func timestamp(now time.Time) string { return fmt.Sprintf("%d", now.Unix()) }

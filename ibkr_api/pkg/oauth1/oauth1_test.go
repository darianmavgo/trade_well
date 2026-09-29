package oauth1

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestEncodeIsRFC3986(t *testing.T) {
	if got := Encode("Ladies + Gentlemen/~a b*c!"); got != "Ladies%20%2B%20Gentlemen%2F~a%20b%2Ac%21" {
		t.Errorf("Encode = %q", got)
	}
}

// The RFC 5849 section 3.4.1 example: the base string is fixed by the RFC.
func TestBaseStringMatchesRFC5849Example(t *testing.T) {
	oauth := Params{
		"oauth_consumer_key":     "9djdj82h48djs9d2",
		"oauth_token":            "kkk9d7dh3k39sjv7",
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        "137131201",
		"oauth_nonce":            "7d8f3e4a",
	}
	q := url.Values{"b5": {"=%3D"}, "a3": {"a", "2 q"}, "c@": {""}, "a2": {"r b"}, "c2": {""}}
	got, err := BaseString("POST", "http://example.com/request?b5=%3D%253D&a3=a&c%40=&a2=r%20b", oauth, q, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "POST&http%3A%2F%2Fexample.com%2Frequest&a2%3Dr%2520b%26a3%3D2%2520q%26a3%3Da%26b5%3D%253D%25253D%26c%2540%3D%26c2%3D%26oauth_consumer_key%3D9djdj82h48djs9d2%26oauth_nonce%3D7d8f3e4a%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D137131201%26oauth_token%3Dkkk9d7dh3k39sjv7"
	if got != want {
		t.Errorf("base string\n got %s\nwant %s", got, want)
	}
}

func TestToByteArrayAddsSignByte(t *testing.T) {
	if got := hex.EncodeToString(ToByteArray(big.NewInt(0x7f))); got != "7f" {
		t.Errorf("0x7f -> %s", got)
	}
	if got := hex.EncodeToString(ToByteArray(big.NewInt(0x80))); got != "0080" {
		t.Errorf("0x80 -> %s", got)
	}
	if got := hex.EncodeToString(ToByteArray(big.NewInt(0x1ff))); got != "01ff" {
		t.Errorf("0x1ff -> %s", got)
	}
}

func testKeys(t *testing.T) (*Keys, []byte, *big.Int) {
	t.Helper()
	sig, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	prime, err := rand.Prime(rand.Reader, 512)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("decrypted-access-token-secret")
	ct, err := rsa.EncryptPKCS1v15(rand.Reader, &enc.PublicKey, secret)
	if err != nil {
		t.Fatal(err)
	}
	return &Keys{ConsumerKey: "TESTCONS1", AccessToken: "tok123", AccessTokenSecret: base64.StdEncoding.EncodeToString(ct),
		Signature: sig, Encryption: enc, DHPrime: prime, DHGenerator: big.NewInt(2)}, secret, prime
}

func TestLiveSessionTokenExchange(t *testing.T) {
	k, secret, p := testKeys(t)
	now := time.Unix(1_700_000_000, 0)
	req, err := k.NewLSTRequest("https://api.ibkr.com/", now)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://api.ibkr.com/v1/api/oauth/live_session_token" || !strings.HasPrefix(req.Header, `OAuth realm="limited_poa"`) {
		t.Fatalf("request = %+v", req)
	}
	for _, want := range []string{`oauth_signature_method="RSA-SHA256"`, `oauth_consumer_key="TESTCONS1"`, `oauth_token="tok123"`, `diffie_hellman_challenge="`, `oauth_timestamp="1700000000"`} {
		if !strings.Contains(req.Header, want) {
			t.Errorf("header lacks %s: %s", want, req.Header)
		}
	}

	// The RSA signature must verify over the base string with the secret's hex prepended.
	params := Params{}
	for _, part := range strings.Split(strings.TrimPrefix(req.Header, `OAuth realm="limited_poa", `), ", ") {
		k, v, _ := strings.Cut(part, "=")
		u, _ := url.QueryUnescape(strings.Trim(v, `"`))
		params[k] = u
	}
	sigB64 := params["oauth_signature"]
	base, _ := BaseString("POST", req.URL, params, url.Values{}, hex.EncodeToString(secret))
	sig, _ := base64.StdEncoding.DecodeString(sigB64)
	sum := sha256.Sum256([]byte(base))
	if err := rsa.VerifyPKCS1v15(&k.Signature.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("RSA signature does not verify: %v", err)
	}

	// The other side's arithmetic per IBKR's description: B = g^b, K = A^b, LST = HMAC-SHA1(K, secret).
	challenge, _ := new(big.Int).SetString(params["diffie_hellman_challenge"], 16)
	b, _ := rand.Int(rand.Reader, new(big.Int).Sub(p, big.NewInt(3)))
	b.Add(b, big.NewInt(2))
	B := new(big.Int).Exp(k.DHGenerator, b, p)
	serverK := new(big.Int).Exp(challenge, b, p)
	serverLST := hmacSHA1(ToByteArray(serverK), secret)
	resp := LSTResponse{
		DiffieHellmanResponse:     hex.EncodeToString(B.Bytes()),
		LiveSessionTokenSignature: hex.EncodeToString(hmacSHA1(serverLST, []byte(k.ConsumerKey))),
		LiveSessionTokenExpiry:    now.Add(24 * time.Hour).UnixMilli(),
	}
	tok, err := k.LiveSessionToken(resp, req.A)
	if err != nil {
		t.Fatalf("LiveSessionToken: %v", err)
	}
	if string(tok.Token) != string(serverLST) || !tok.Expires.Equal(now.Add(24*time.Hour).UTC()) {
		t.Errorf("token mismatch or wrong expiry %v", tok.Expires)
	}

	resp.LiveSessionTokenSignature = strings.Repeat("0", 40)
	if _, err := k.LiveSessionToken(resp, req.A); err == nil {
		t.Error("a wrong live_session_token_signature must be rejected")
	}

	// A request signed with the token: HMAC-SHA256 over the base string, recomputable.
	q := url.Values{"conids": {"265598"}}
	hdr, err := k.Sign("GET", "https://api.ibkr.com/v1/api/iserver/marketdata/snapshot", q, tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hdr, `oauth_signature_method="HMAC-SHA256"`) || strings.Contains(hdr, "diffie_hellman") {
		t.Errorf("api header = %s", hdr)
	}
	m := regexpValue(hdr, "oauth_nonce")
	p2 := Params{"oauth_consumer_key": "TESTCONS1", "oauth_nonce": m, "oauth_signature_method": MethodHMACSHA256, "oauth_timestamp": "1700000000", "oauth_token": "tok123"}
	b2, _ := BaseString("GET", "https://api.ibkr.com/v1/api/iserver/marketdata/snapshot", p2, q, "")
	wantSig := SignHMACSHA256(tok.Token, b2)
	if !strings.Contains(hdr, `oauth_signature="`+Encode(wantSig)+`"`) {
		t.Errorf("signature does not match the recomputed one\n%s", hdr)
	}
}

func regexpValue(header, name string) string {
	i := strings.Index(header, name+`="`)
	rest := header[i+len(name)+2:]
	return rest[:strings.Index(rest, `"`)]
}

func TestParseDHParamsAndKeys(t *testing.T) {
	der, err := asn1.Marshal(struct{ P, G *big.Int }{big.NewInt(23), big.NewInt(5)})
	if err != nil {
		t.Fatal(err)
	}
	p, g, err := ParseDHParams(string(pem.EncodeToMemory(&pem.Block{Type: "DH PARAMETERS", Bytes: der})))
	if err != nil || p.Int64() != 23 || g.Int64() != 5 {
		t.Fatalf("p=%v g=%v err=%v", p, g, err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pk8, _ := x509.MarshalPKCS8PrivateKey(key)
	got, err := ParseRSAPrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk8})))
	if err != nil || got.N.Cmp(key.N) != 0 {
		t.Errorf("PKCS#8 parse: %v", err)
	}
}

package ibkr

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestSignedJWTVerifiesWithThePublicKeyAndKeyParsesFromBase64(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	for name, in := range map[string]string{"pem": pemText, "base64": base64.StdEncoding.EncodeToString([]byte(pemText))} {
		got, err := ParsePrivateKey(in)
		if err != nil || got.N.Cmp(key.N) != 0 {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	jwt, err := SignJWT(key, "kid1", map[string]any{"iss": "c", "aud": "a"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %q", jwt)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	var hdr map[string]string
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(h, &hdr)
	if hdr["alg"] != "RS256" || hdr["kid"] != "kid1" {
		t.Errorf("header = %v", hdr)
	}
}

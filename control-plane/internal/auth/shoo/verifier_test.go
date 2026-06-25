package shoo

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifierAcceptsShooToken(t *testing.T) {
	key, jwks := testKey(t)
	now := time.Unix(1_700_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	token := signToken(t, key, map[string]any{
		"iss":          "https://shoo.dev",
		"aud":          "origin:http://localhost:8080",
		"sub":          "ps_test",
		"pairwise_sub": "ps_test",
		"iat":          now.Unix(),
		"exp":          now.Add(time.Hour).Unix(),
		"jti":          "jwt-1",
		"email":        "dev@example.com",
	})

	verifier, err := NewVerifier(Config{
		BaseURL:   srv.URL,
		AppOrigin: "http://localhost:8080/dashboard",
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.PairwiseSub != "ps_test" || claims.Email != "dev@example.com" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestVerifierRejectsWrongAudience(t *testing.T) {
	key, jwks := testKey(t)
	now := time.Unix(1_700_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	token := signToken(t, key, map[string]any{
		"iss":          "https://shoo.dev",
		"aud":          "origin:https://other.example",
		"sub":          "ps_test",
		"pairwise_sub": "ps_test",
		"iat":          now.Unix(),
		"exp":          now.Add(time.Hour).Unix(),
	})
	verifier, err := NewVerifier(Config{
		BaseURL:   srv.URL,
		AppOrigin: "http://localhost:8080",
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("Verify wrong audience error = %v", err)
	}
}

func testKey(t *testing.T) (*ecdsa.PrivateKey, map[string]any) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"kid": "test-key",
		"x":   base64.RawURLEncoding.EncodeToString(leftPad(key.X.Bytes(), size)),
		"y":   base64.RawURLEncoding.EncodeToString(leftPad(key.Y.Bytes(), size)),
	}
	return key, map[string]any{"keys": []any{jwk}}
}

func signToken(t *testing.T, key *ecdsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": "test-key"}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	sig := append(leftPad(r.Bytes(), size), leftPad(s.Bytes(), size)...)
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func leftPad(in []byte, size int) []byte {
	if len(in) >= size {
		return in
	}
	out := make([]byte, size)
	copy(out[size-len(in):], in)
	return out
}

var _ = big.Int{}

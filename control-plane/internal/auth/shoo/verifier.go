// Package shoo verifies id_tokens issued by https://shoo.dev.
package shoo

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://shoo.dev"
	DefaultIssuer  = "https://shoo.dev"
)

var (
	ErrInvalidToken = errors.New("shoo: invalid token")
	ErrNoKey        = errors.New("shoo: signing key not found")
)

type Config struct {
	BaseURL   string
	Issuer    string
	AppOrigin string
	Client    *http.Client
	Now       func() time.Time
	Leeway    time.Duration
}

type Verifier struct {
	baseURL   string
	issuer    string
	appOrigin string
	client    *http.Client
	now       func() time.Time
	leeway    time.Duration

	mu   sync.RWMutex
	keys map[string]*ecdsa.PublicKey
}

type Claims struct {
	Subject       string    `json:"sub"`
	PairwiseSub   string    `json:"pairwise_sub"`
	Email         string    `json:"email,omitempty"`
	EmailVerified bool      `json:"email_verified,omitempty"`
	Name          string    `json:"name,omitempty"`
	Picture       string    `json:"picture,omitempty"`
	JWTID         string    `json:"jti"`
	IssuedAt      time.Time `json:"iat"`
	ExpiresAt     time.Time `json:"exp"`
}

func NewVerifier(cfg Config) (*Verifier, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Issuer == "" {
		cfg.Issuer = DefaultIssuer
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.AppOrigin == "" {
		return nil, fmt.Errorf("%w: app origin is required", ErrInvalidToken)
	}
	origin, err := normalizeOrigin(cfg.AppOrigin)
	if err != nil {
		return nil, err
	}
	return &Verifier{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		issuer:    strings.TrimRight(cfg.Issuer, "/"),
		appOrigin: origin,
		client:    cfg.Client,
		now:       cfg.Now,
		leeway:    cfg.Leeway,
		keys:      make(map[string]*ecdsa.PublicKey),
	}, nil
}

func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: expected three JWT segments", ErrInvalidToken)
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return Claims{}, err
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return Claims{}, fmt.Errorf("%w: header: %v", ErrInvalidToken, err)
	}
	if header.Alg != "ES256" {
		return Claims{}, fmt.Errorf("%w: unsupported alg %q", ErrInvalidToken, header.Alg)
	}
	if header.Kid == "" {
		return Claims{}, fmt.Errorf("%w: missing kid", ErrInvalidToken)
	}

	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return Claims{}, err
	}
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return Claims{}, err
	}
	if len(sig) != 64 {
		return Claims{}, fmt.Errorf("%w: ES256 signature must be 64 bytes", ErrInvalidToken)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(key, hash[:], r, s) {
		return Claims{}, fmt.Errorf("%w: signature verification failed", ErrInvalidToken)
	}

	payloadBytes, err := decodeSegment(parts[1])
	if err != nil {
		return Claims{}, err
	}
	return v.validatePayload(payloadBytes)
}

func (v *Verifier) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	v.mu.RLock()
	key := v.keys[kid]
	v.mu.RUnlock()
	if key != nil {
		return key, nil
	}
	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}
	v.mu.RLock()
	key = v.keys[kid]
	v.mu.RUnlock()
	if key == nil {
		return nil, fmt.Errorf("%w: %q", ErrNoKey, kid)
	}
	return key, nil
}

func (v *Verifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/.well-known/jwks.json", nil)
	if err != nil {
		return err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("shoo: JWKS returned %s", res.Status)
	}
	var body struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	keys := make(map[string]*ecdsa.PublicKey, len(body.Keys))
	for _, raw := range body.Keys {
		key, err := raw.ecdsaKey()
		if err != nil {
			return err
		}
		keys[raw.Kid] = key
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j jwk) ecdsaKey() (*ecdsa.PublicKey, error) {
	if j.Kty != "EC" || j.Crv != "P-256" || j.Kid == "" {
		return nil, fmt.Errorf("shoo: unsupported JWK kid=%q kty=%q crv=%q", j.Kid, j.Kty, j.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(j.X)
	if err != nil {
		return nil, err
	}
	y, err := base64.RawURLEncoding.DecodeString(j.Y)
	if err != nil {
		return nil, err
	}
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !key.Curve.IsOnCurve(key.X, key.Y) {
		return nil, fmt.Errorf("shoo: JWK point is not on P-256")
	}
	return key, nil
}

type rawClaims struct {
	Issuer        string          `json:"iss"`
	Audience      json.RawMessage `json:"aud"`
	Subject       string          `json:"sub"`
	PairwiseSub   string          `json:"pairwise_sub"`
	Email         string          `json:"email"`
	EmailVerified bool            `json:"email_verified"`
	Name          string          `json:"name"`
	Picture       string          `json:"picture"`
	JWTID         string          `json:"jti"`
	IssuedAt      int64           `json:"iat"`
	NotBefore     int64           `json:"nbf"`
	ExpiresAt     int64           `json:"exp"`
}

func (v *Verifier) validatePayload(payload []byte) (Claims, error) {
	var raw rawClaims
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("%w: payload: %v", ErrInvalidToken, err)
	}
	if raw.Issuer != v.issuer {
		return Claims{}, fmt.Errorf("%w: issuer %q", ErrInvalidToken, raw.Issuer)
	}
	if !audienceContains(raw.Audience, "origin:"+v.appOrigin) {
		return Claims{}, fmt.Errorf("%w: wrong audience", ErrInvalidToken)
	}
	now := v.now()
	if raw.ExpiresAt == 0 || time.Unix(raw.ExpiresAt, 0).Before(now.Add(-v.leeway)) {
		return Claims{}, fmt.Errorf("%w: token expired", ErrInvalidToken)
	}
	if raw.NotBefore != 0 && time.Unix(raw.NotBefore, 0).After(now.Add(v.leeway)) {
		return Claims{}, fmt.Errorf("%w: token not valid yet", ErrInvalidToken)
	}
	if raw.IssuedAt != 0 && time.Unix(raw.IssuedAt, 0).After(now.Add(v.leeway)) {
		return Claims{}, fmt.Errorf("%w: token issued in the future", ErrInvalidToken)
	}
	if raw.PairwiseSub == "" {
		return Claims{}, fmt.Errorf("%w: missing pairwise_sub", ErrInvalidToken)
	}
	if raw.Subject == "" {
		raw.Subject = raw.PairwiseSub
	}
	return Claims{
		Subject:       raw.Subject,
		PairwiseSub:   raw.PairwiseSub,
		Email:         raw.Email,
		EmailVerified: raw.EmailVerified,
		Name:          raw.Name,
		Picture:       raw.Picture,
		JWTID:         raw.JWTID,
		IssuedAt:      time.Unix(raw.IssuedAt, 0),
		ExpiresAt:     time.Unix(raw.ExpiresAt, 0),
	}, nil
}

func audienceContains(raw json.RawMessage, want string) bool {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == want
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return false
	}
	for _, aud := range many {
		if aud == want {
			return true
		}
	}
	return false
}

func normalizeOrigin(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: app origin must include scheme and host", ErrInvalidToken)
	}
	return u.Scheme + "://" + u.Host, nil
}

func decodeSegment(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: base64url: %v", ErrInvalidToken, err)
	}
	return b, nil
}

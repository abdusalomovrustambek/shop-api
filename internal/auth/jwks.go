// Package auth verifies the bearer tokens issued by auth-gateway: EdDSA JWTs checked
// against the gateway's JWKS, using only the standard library.
package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// keyTTL is how long a fetched key set is trusted.
	keyTTL = 10 * time.Minute
	// minRefetch is the shortest time between two fetch attempts, so unknown key ids
	// or a dead endpoint cannot turn every request into a fetch.
	minRefetch   = 5 * time.Second
	maxJWKSBytes = 1 << 20
)

var (
	errInvalidToken    = errors.New("invalid token")
	errUnknownKey      = errors.New("unknown signing key")
	errKeysUnavailable = errors.New("signing keys unavailable")
)

// Verifier checks tokens against a JWKS endpoint and caches the keys.
type Verifier struct {
	url    string
	client *http.Client
	log    *slog.Logger
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]ed25519.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	lastOK      bool
}

// NewVerifier returns a Verifier that reads keys from jwksURL.
func NewVerifier(jwksURL string, log *slog.Logger) *Verifier {
	return &Verifier{
		url:    jwksURL,
		client: &http.Client{Timeout: 5 * time.Second},
		log:    log,
		now:    time.Now,
	}
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type tokenClaims struct {
	Sub string   `json:"sub"`
	Exp *float64 `json:"exp"`
	Nbf *float64 `json:"nbf"`
}

// Verify checks the token's signature, alg, exp and nbf and returns the user id (the
// UUID in sub). Failures wrap errInvalidToken, except when no keys can be loaded.
func (v *Verifier) Verify(ctx context.Context, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: not a JWT", errInvalidToken)
	}
	var header tokenHeader
	if err := decodePart(parts[0], &header); err != nil {
		return "", fmt.Errorf("%w: header: %v", errInvalidToken, err)
	}
	if header.Alg != "EdDSA" {
		return "", fmt.Errorf("%w: alg %q", errInvalidToken, header.Alg)
	}
	if header.Kid == "" {
		return "", fmt.Errorf("%w: missing kid", errInvalidToken)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("%w: signature encoding", errInvalidToken)
	}

	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return "", err
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return "", fmt.Errorf("%w: bad signature", errInvalidToken)
	}

	var claims tokenClaims
	if err := decodePart(parts[1], &claims); err != nil {
		return "", fmt.Errorf("%w: claims: %v", errInvalidToken, err)
	}
	now := float64(v.now().Unix())
	if claims.Exp == nil || now >= *claims.Exp {
		return "", fmt.Errorf("%w: expired", errInvalidToken)
	}
	if claims.Nbf != nil && now < *claims.Nbf {
		return "", fmt.Errorf("%w: not yet valid", errInvalidToken)
	}
	if !isUUID(claims.Sub) {
		return "", fmt.Errorf("%w: sub is not a UUID", errInvalidToken)
	}
	return claims.Sub, nil
}

func decodePart(part string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// key returns the public key for kid. Cached keys keep working when the endpoint is
// down; an unknown kid triggers one refetch.
func (v *Verifier) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.now()
	cached, known := v.keys[kid]
	fresh := len(v.keys) > 0 && now.Sub(v.fetchedAt) < keyTTL
	if known && fresh {
		return cached, nil
	}
	if !v.lastAttempt.IsZero() && now.Sub(v.lastAttempt) < minRefetch {
		return v.fallback(cached, known)
	}

	v.lastAttempt = now
	err := v.refresh(ctx)
	v.lastOK = err == nil
	if err != nil {
		v.log.Error("fetch jwks", "err", err, "url", v.url)
		return v.fallback(cached, known)
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, errUnknownKey
}

// fallback answers when no fetch is made or the fetch failed: a known (possibly stale)
// key is still good, otherwise the failure tells whether the kid is unknown or the keys are out of reach.
func (v *Verifier) fallback(cached ed25519.PublicKey, known bool) (ed25519.PublicKey, error) {
	if known {
		return cached, nil
	}
	if v.lastOK {
		return nil, errUnknownKey
	}
	return nil, errKeysUnavailable
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

// refresh replaces the cached key set. The caller holds v.mu.
func (v *Verifier) refresh(ctx context.Context) error {
	// One client giving up must not fail the fetch for everyone waiting behind the mutex.
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodGet, v.url, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	keys := make(map[string]ed25519.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(x)
	}
	if len(keys) == 0 {
		return errors.New("jwks has no usable Ed25519 keys")
	}
	v.keys = keys
	v.fetchedAt = v.now()
	return nil
}

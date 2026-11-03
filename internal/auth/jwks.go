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
	inflight    chan struct{}
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

// key returns the public key for kid. A fresh cached key is returned at once. Otherwise
// one fetch runs at a time, outside the mutex; a caller that already holds the key (stale)
// does not wait for it, and one that does not waits only until ctx ends. Cached keys keep
// working when the endpoint is down; an unknown kid triggers one refetch.
func (v *Verifier) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	v.mu.Lock()
	now := v.now()
	cached, known := v.keys[kid]
	fresh := len(v.keys) > 0 && now.Sub(v.fetchedAt) < keyTTL
	if known && fresh {
		v.mu.Unlock()
		return cached, nil
	}

	done := v.inflight
	switch {
	case done != nil && known:
		v.mu.Unlock()
		return cached, nil
	case done == nil:
		if !v.lastAttempt.IsZero() && now.Sub(v.lastAttempt) < minRefetch {
			key, err := v.fallback(cached, known)
			v.mu.Unlock()
			return key, err
		}
		done = make(chan struct{})
		v.inflight = done
		// The fetch belongs to the verifier, not to the request that started it.
		go v.refresh(context.WithoutCancel(ctx), done)
	}
	v.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		if known {
			return cached, nil
		}
		return nil, fmt.Errorf("%w: %v", errKeysUnavailable, ctx.Err())
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fallback(v.keys[kid], v.keys[kid] != nil)
}

// fallback answers once no (further) fetch will be made: a known (possibly stale) key is
// still good, otherwise the last fetch tells whether the kid is unknown or the keys are out
// of reach. The caller holds v.mu.
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

// refresh fetches the key set, stores the result and closes done. The 5 second client
// timeout bounds it.
func (v *Verifier) refresh(ctx context.Context, done chan struct{}) {
	keys, err := v.fetch(ctx)

	v.mu.Lock()
	v.inflight = nil
	v.lastAttempt = v.now()
	v.lastOK = err == nil
	if err == nil {
		v.keys = keys
		v.fetchedAt = v.lastAttempt
	}
	v.mu.Unlock()

	if err != nil {
		v.log.Error("fetch jwks", "err", err, "url", v.url)
	}
	close(done)
}

// fetch downloads and parses the JWKS.
func (v *Verifier) fetch(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJWKSBytes)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
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
		return nil, errors.New("jwks has no usable Ed25519 keys")
	}
	return keys, nil
}

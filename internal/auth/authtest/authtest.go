// Package authtest is a fake auth-gateway for tests: it serves a JWKS and signs tokens.
package authtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// DefaultKID is the id of the key every Issuer starts with.
const DefaultKID = "k1"

// Issuer serves a JWKS over HTTP and signs tokens with the keys it holds.
type Issuer struct {
	server *httptest.Server

	mu        sync.Mutex
	keys      map[string]ed25519.PrivateKey
	published map[string]bool
	down      bool
	hold      *hold
	fetches   int
}

// NewIssuer starts the JWKS server with one published key, DefaultKID. It stops with the test.
func NewIssuer(t testing.TB) *Issuer {
	t.Helper()
	i := &Issuer{keys: map[string]ed25519.PrivateKey{}, published: map[string]bool{}}
	i.AddKey(t, DefaultKID, true)
	i.server = httptest.NewServer(http.HandlerFunc(i.serveJWKS))
	t.Cleanup(i.server.Close)
	return i
}

// URL is the JWKS endpoint.
func (i *Issuer) URL() string { return i.server.URL }

// Fetches counts the JWKS requests served so far.
func (i *Issuer) Fetches() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fetches
}

// SetDown makes the JWKS endpoint answer 503 (or recover).
func (i *Issuer) SetDown(down bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.down = down
}

// AddKey creates a key; it appears in the JWKS only when published.
func (i *Issuer) AddKey(t testing.TB, kid string, published bool) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys[kid] = priv
	i.published[kid] = published
}

// Publish adds an existing key to the JWKS.
func (i *Issuer) Publish(kid string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.published[kid] = true
}

type hold struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

// Hold makes the JWKS endpoint stall: every request waits until release is called (or the
// client gives up). started receives a value when the first stalled request arrives.
func (i *Issuer) Hold() (started <-chan struct{}, release func()) {
	h := &hold{started: make(chan struct{}, 1), release: make(chan struct{})}
	i.mu.Lock()
	i.hold = h
	i.mu.Unlock()
	return h.started, func() { h.once.Do(func() { close(h.release) }) }
}

func (i *Issuer) serveJWKS(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	h := i.hold
	i.mu.Unlock()
	if h != nil {
		select {
		case h.started <- struct{}{}:
		default:
		}
		select {
		case <-h.release:
		case <-r.Context().Done():
			return
		}
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	i.fetches++
	if i.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	type jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		Kid string `json:"kid"`
		X   string `json:"x"`
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	doc.Keys = []jwk{}
	for kid, priv := range i.keys {
		if !i.published[kid] {
			continue
		}
		pub := ed25519.PublicKey(priv[ed25519.SeedSize:])
		doc.Keys = append(doc.Keys, jwk{Kty: "OKP", Crv: "Ed25519", Kid: kid, X: base64.RawURLEncoding.EncodeToString(pub)})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
	}
}

// Sign builds a token with the given header and claims, signed by the key signKID.
func (i *Issuer) Sign(t testing.TB, signKID string, header, claims map[string]any) string {
	t.Helper()
	i.mu.Lock()
	priv, ok := i.keys[signKID]
	i.mu.Unlock()
	if !ok {
		t.Fatalf("no key %q", signKID)
	}
	enc := func(v map[string]any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signingInput := enc(header) + "." + enc(claims)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// UserToken returns a valid token for the user (a UUID), good for one hour.
func (i *Issuer) UserToken(t testing.TB, sub string) string {
	t.Helper()
	return i.Sign(t, DefaultKID,
		map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": DefaultKID},
		map[string]any{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()})
}

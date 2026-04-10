package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const jwksTTL = 5 * time.Minute

// JWKSFetcher fetches and caches a JSON Web Key Set from a remote URL,
// refreshing it after jwksTTL has elapsed.
type JWKSFetcher struct {
	url    string
	client *http.Client
	mu     sync.RWMutex
	jwks   *jose.JSONWebKeySet
	expiry time.Time
}

// GetJWKS returns the cached JWKS if still valid, or fetches a fresh copy from
// the remote URL and resets the TTL.
func (f *JWKSFetcher) GetJWKS() (*jose.JSONWebKeySet, error) {
	f.mu.RLock()
	if f.jwks != nil && time.Now().Before(f.expiry) {
		jwks := f.jwks
		f.mu.RUnlock()
		return jwks, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jwks != nil && time.Now().Before(f.expiry) {
		return f.jwks, nil
	}
	jwks, err := f.fetch()
	if err != nil {
		return nil, err
	}
	f.jwks = jwks
	f.expiry = time.Now().Add(jwksTTL)
	return f.jwks, nil
}

// fetchJWKS retrieves and parses the JWKS from the given URL.
func (f *JWKSFetcher) fetch() (*jose.JSONWebKeySet, error) {
	resp, err := f.client.Get(f.url)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var jwks jose.JSONWebKeySet
	err = json.NewDecoder(resp.Body).Decode(&jwks)
	if err != nil {
		return nil, err
	}
	return &jwks, nil
}

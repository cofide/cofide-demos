package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OIDCDiscovery holds the subset of OIDC discovery document fields used by this application.
type OIDCDiscovery struct {
	TokenEndpoint string `json:"token_endpoint"`
	JWKSUri       string `json:"jwks_uri"`
}

// Discover fetches and parses the OIDC discovery document for the given issuer URL.
func Discover(issuerURL string, client *http.Client) (*OIDCDiscovery, error) {
	issuerURL = strings.TrimRight(issuerURL, "/")
	discoveryURL := issuerURL + "/.well-known/openid-configuration"

	resp, err := client.Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OIDC discovery document: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery request failed with status %d", resp.StatusCode)
	}

	var doc OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse OIDC discovery document: %w", err)
	}

	if doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery document missing token_endpoint")
	}
	if doc.JWKSUri == "" {
		return nil, fmt.Errorf("OIDC discovery document missing jwks_uri")
	}

	return &doc, nil
}

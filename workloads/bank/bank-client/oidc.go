package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// oidcDiscovery holds the subset of an OIDC discovery document used here.
// Nothing in this file is specific to any one IdP — it's plain OIDC
// Discovery 1.0 + Authorization Code + PKCE, so it works the same whether
// the discovery URL points at Ory, Auth0, Okta, Keycloak, Cognito, etc.
type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// discoverOIDC fetches and parses the IdP's OIDC discovery document.
// discoveryURL must be the full discovery document URL (ending in
// /.well-known/openid-configuration), not just the issuer — this matches
// the shape terraform/bootstrap's oidc_discovery_url output already
// produces (required as-is by AWS AgentCore's custom_jwt_authorizer, see
// terraform/agentcore.tf), so this function must not append the suffix
// itself on top of that.
func discoverOIDC(discoveryURL string, client *http.Client) (*oidcDiscovery, error) {
	resp, err := client.Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OIDC discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery request failed with status %d", resp.StatusCode)
	}

	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse OIDC discovery document: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery document missing authorization_endpoint or token_endpoint")
	}
	return &doc, nil
}

// authorizationURL builds the URL to redirect the user's browser to for the
// IdP's OIDC Authorization Code flow with PKCE.
func (o *oidcDiscovery) authorizationURL(clientID, redirectURL, state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid offline_access")
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return o.AuthorizationEndpoint + "?" + q.Encode()
}

// tokenResponse holds the fields used from the IdP's token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

// exchangeCode exchanges an authorization code for tokens at the IdP's token
// endpoint. bank-client is registered as a public OAuth2 client — no
// client_secret — so the PKCE code_verifier is what proves this request
// comes from whoever started the flow, rather than a long-lived shared
// secret sitting in a Kubernetes Secret.
func (o *oidcDiscovery) exchangeCode(client *http.Client, clientID, redirectURL, code, codeVerifier string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURL)
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequest(http.MethodPost, o.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach OIDC token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC token exchange failed with status %d", resp.StatusCode)
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("failed to parse OIDC token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("OIDC token response missing access_token")
	}
	return &tok, nil
}

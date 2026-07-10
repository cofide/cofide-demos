package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const jwksTTL = 5 * time.Minute

var allowedSignatureAlgs = []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512, jose.ES256, jose.ES384, jose.ES512}

// userAgentTransport wraps an http.RoundTripper to set a custom User-Agent on
// every outgoing request. A nil rt uses http.DefaultTransport, matching
// http.Client's own zero-value behavior.
type userAgentTransport struct {
	rt        http.RoundTripper
	userAgent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	rt := t.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}

// JWKSFetcher fetches and caches a JSON Web Key Set from a remote URL,
// refreshing it after jwksTTL has elapsed.
type JWKSFetcher struct {
	url    string
	client *http.Client
	mu     sync.RWMutex
	jwks   *jose.JSONWebKeySet
	expiry time.Time
}

// GetJWKS returns the cached JWKS if still valid, or fetches a fresh copy.
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

func (f *JWKSFetcher) fetch() (*jose.JSONWebKeySet, error) {
	resp, err := f.client.Get(f.url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var jwks jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, err
	}
	return &jwks, nil
}

// discoverJWKSURI fetches the jwks_uri from an OIDC discovery document, the
// same way workloads/ping-pong-exchange discovers Credex's token/JWKS
// endpoints. discoveryURL must be the full discovery document URL (ending in
// /.well-known/openid-configuration), not just the issuer — this matches
// AWS's own AgentCore Credential Provider, which requires the same full,
// suffixed form for its discovery_url (enforced server-side by a regex), so
// terraform/agentcore.tf's credex_discovery_url is already shaped this way
// for that consumer; this function must not append the suffix itself on
// top of that.
func discoverJWKSURI(discoveryURL string, client *http.Client) (string, error) {
	resp, err := client.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OIDC discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OIDC discovery request failed with status %d", resp.StatusCode)
	}

	var doc struct {
		JWKSUri string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("failed to parse OIDC discovery document: %w", err)
	}
	if doc.JWKSUri == "" {
		return "", fmt.Errorf("OIDC discovery document missing jwks_uri")
	}
	return doc.JWKSUri, nil
}

// actorClaim represents the RFC 8693 "act" (actor) claim in a delegated token.
type actorClaim struct {
	Sub string `json:"sub"`
}

// delegatedClaims extends the standard JWT claims with the RFC 8693 "act" claim.
type delegatedClaims struct {
	jwt.Claims
	Act *actorClaim `json:"act,omitempty"`
}

// delegatedJWTAuthMiddleware authorises requests bearing a Credex-minted,
// delegated access token: bank-agent's on-behalf-of exchange (see
// terraform/agentcore.tf) mints a token whose top-level "sub" is the
// signed-in customer's identity from their IdP — not a SPIFFE ID — and
// whose "act.sub" is bank-agent's own identity. That's why this can't reuse
// jwtSVIDAuthMiddleware: the token isn't a JWT-SVID validated against the
// local SPIFFE Workload API, it's an ordinary OAuth2 access token validated
// against Credex's own published JWKS, the same way
// workloads/ping-pong-exchange's relay mode validates delegated tokens.
//
// authorizedActor is compared as a plain string, not parsed as a SPIFFE ID:
// bank-agent's actor identity in "act.sub" is whatever Credex's delegated
// exchange put there, which for the current AgentCore On-Behalf-Of flow
// (terraform/agentcore.tf's M2M actorTokenContent) is the AWS IAM role ARN
// of bank-agent's execution role, not a spiffe:// URI — Credex doesn't
// normalize AWS-federated identities into SPIFFE IDs anywhere in this path.
func delegatedJWTAuthMiddleware(jwksFetcher *JWKSFetcher, expectedAudience string, authorizedActor string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "missing bearer token")
			http.Error(w, "no token provided", http.StatusUnauthorized)
			return
		}

		jwks, err := jwksFetcher.GetJWKS()
		if err != nil {
			slog.Error("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "failed to fetch Credex JWKS", "error", err)
			http.Error(w, "unable to fetch JWKS", http.StatusServiceUnavailable)
			return
		}

		tok, err := jwt.ParseSigned(token, allowedSignatureAlgs)
		if err != nil {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "failed to parse token", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		var claims delegatedClaims
		if err := tok.Claims(jwks, &claims); err != nil {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "failed signature verification against Credex JWKS", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if err := claims.ValidateWithLeeway(jwt.Expected{Time: time.Now()}, 0); err != nil {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "failed time validation", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if !slices.Contains([]string(claims.Audience), expectedAudience) {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "wrong audience", "audience", claims.Audience, "expected", expectedAudience)
			http.Error(w, "invalid audience in token", http.StatusUnauthorized)
			return
		}

		if claims.Act == nil || claims.Act.Sub == "" {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "missing act claim")
			http.Error(w, "missing act claim", http.StatusUnauthorized)
			return
		}
		if claims.Act.Sub != authorizedActor {
			slog.Warn("Rejected request", "mechanism", "delegated_jwt", "caller", "bank-agent", "reason", "unauthorized actor", "actor", claims.Act.Sub)
			http.Error(w, "unauthorized actor", http.StatusForbidden)
			return
		}

		onBehalfOf := claims.Subject
		if onBehalfOf == "" {
			onBehalfOf = "unknown"
		}
		slog.Info("Authorised request", "mechanism", "delegated_jwt", "caller", "bank-agent", "on_behalf_of_verified", onBehalfOf, "actor", claims.Act.Sub)

		next(w, r)
	}
}

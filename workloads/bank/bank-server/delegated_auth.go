package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

const jwksTTL = 5 * time.Minute

var allowedSignatureAlgs = []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512, jose.ES256, jose.ES384, jose.ES512}

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
// endpoints.
func discoverJWKSURI(issuerURL string, client *http.Client) (string, error) {
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"

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
func delegatedJWTAuthMiddleware(jwksFetcher *JWKSFetcher, expectedAudience string, authorizedActor spiffeid.ID, next http.HandlerFunc) http.HandlerFunc {
	matchActor := spiffeid.MatchID(authorizedActor)
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			http.Error(w, "no token provided", http.StatusUnauthorized)
			return
		}

		jwks, err := jwksFetcher.GetJWKS()
		if err != nil {
			slog.Error("Failed to fetch Credex JWKS", "error", err)
			http.Error(w, "unable to fetch JWKS", http.StatusServiceUnavailable)
			return
		}

		tok, err := jwt.ParseSigned(token, allowedSignatureAlgs)
		if err != nil {
			slog.Warn("Failed to parse delegated token", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		var claims delegatedClaims
		if err := tok.Claims(jwks, &claims); err != nil {
			slog.Warn("Failed to verify delegated token", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if err := claims.ValidateWithLeeway(jwt.Expected{Time: time.Now()}, 0); err != nil {
			slog.Warn("Delegated token failed time validation", "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		if !slices.Contains([]string(claims.Audience), expectedAudience) {
			slog.Warn("Invalid audience in delegated token", "audience", claims.Audience, "expected", expectedAudience)
			http.Error(w, "invalid audience in token", http.StatusUnauthorized)
			return
		}

		if claims.Act == nil || claims.Act.Sub == "" {
			slog.Warn("Missing act claim in delegated token")
			http.Error(w, "missing act claim", http.StatusUnauthorized)
			return
		}
		actorID, err := spiffeid.FromString(claims.Act.Sub)
		if err != nil {
			slog.Warn("Invalid act.sub in delegated token", "act_sub", claims.Act.Sub)
			http.Error(w, "invalid actor", http.StatusUnauthorized)
			return
		}
		if err := matchActor(actorID); err != nil {
			slog.Warn("Rejected unauthorized actor", "actor", claims.Act.Sub)
			http.Error(w, "unauthorized actor", http.StatusForbidden)
			return
		}

		onBehalfOf := claims.Subject
		if onBehalfOf == "" {
			onBehalfOf = "unknown"
		}
		slog.Info("Authorised bank-agent request", "on_behalf_of_verified", onBehalfOf, "actor", claims.Act.Sub)

		next(w, r)
	}
}

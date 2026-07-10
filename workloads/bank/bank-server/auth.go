package main

import (
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	return strings.TrimPrefix(auth, prefix), true
}

// staticAuthMiddleware authorises requests bearing a pre-shared API key.
// caller names the workload the key identifies (e.g. "bank-client",
// "bank-lambda"), purely for logging.
func staticAuthMiddleware(caller, expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			slog.Warn("Rejected request", "auth_method", "static-secret", "caller", caller, "reason", "missing bearer token")
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			slog.Warn("Rejected request", "auth_method", "static-secret", "caller", caller, "reason", "wrong API key")
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		slog.Info("Authorised request", "auth_method", "static-secret", "caller", caller)
		next(w, r)
	}
}

// staticAgentAuthMiddleware authorises bank-agent's requests bearing a
// pre-shared API key, the same as staticAuthMiddleware, but also logs the
// caller's identity from the X-On-Behalf-Of header. Unlike the delegated
// JWT bank-agent presents in spiffe mode, this header is asserted by
// bank-agent, not cryptographically verified — that distinction is the
// point of the static/spiffe toggle, and is called out explicitly in the
// log field name below.
func staticAgentAuthMiddleware(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			slog.Warn("Rejected request", "auth_method", "static-secret", "caller", "bank-agent", "reason", "missing bearer token")
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			slog.Warn("Rejected request", "auth_method", "static-secret", "caller", "bank-agent", "reason", "wrong API key")
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		onBehalfOf := r.Header.Get("X-On-Behalf-Of")
		if onBehalfOf == "" {
			onBehalfOf = "unknown"
		}
		slog.Info("Authorised request", "auth_method", "static-secret", "caller", "bank-agent", "on_behalf_of_asserted_unverified", onBehalfOf)
		next(w, r)
	}
}

// jwtSVIDAuthMiddleware authorises requests bearing a JWT-SVID scoped to the
// given audience and issued to the given SPIFFE ID, validated against the
// local SPIFFE Workload API.
func jwtSVIDAuthMiddleware(wlClient *workloadapi.Client, audience string, authorizedID spiffeid.ID, next http.HandlerFunc) http.HandlerFunc {
	matcher := spiffeid.MatchID(authorizedID)
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			slog.Warn("Rejected request", "auth_method", "jwt-svid", "caller", "bank-lambda", "reason", "missing bearer token")
			http.Error(w, "no token provided", http.StatusUnauthorized)
			return
		}

		svid, err := wlClient.ValidateJWTSVID(r.Context(), token, audience)
		if err != nil {
			slog.Warn("Rejected request", "auth_method", "jwt-svid", "caller", "bank-lambda", "reason", "invalid JWT-SVID", "error", err)
			http.Error(w, fmt.Sprintf("invalid token: %s", err), http.StatusUnauthorized)
			return
		}

		if err := matcher(svid.ID); err != nil {
			slog.Warn("Rejected request", "auth_method", "jwt-svid", "caller", "bank-lambda", "reason", "unauthorized SPIFFE ID", "spiffe_id", svid.ID.String())
			http.Error(w, "unauthorized SPIFFE ID", http.StatusForbidden)
			return
		}

		slog.Info("Authorised request", "auth_method", "jwt-svid", "caller", "bank-lambda", "spiffe_id", svid.ID.String())
		next(w, r)
	}
}

// loggingAuthorizer wraps a go-spiffe tlsconfig.Authorizer to log the mTLS
// authorization decision — success or failure — including the peer's SPIFFE
// ID. Without this, an mTLS handshake accepted or rejected by the TLS layer
// leaves no trace: the standard library's http.Server never invokes any
// handler for a rejected handshake, so there's nowhere else to log this.
// caller names the workload the peer is expected to be, purely for logging.
func loggingAuthorizer(caller string, inner tlsconfig.Authorizer) tlsconfig.Authorizer {
	return func(id spiffeid.ID, verifiedChains [][]*x509.Certificate) error {
		if err := inner(id, verifiedChains); err != nil {
			slog.Warn("Rejected mTLS handshake", "auth_method", "mtls", "caller", caller, "spiffe_id", id.String(), "error", err)
			return err
		}
		slog.Info("Authorised mTLS handshake", "auth_method", "mtls", "caller", caller, "spiffe_id", id.String())
		return nil
	}
}

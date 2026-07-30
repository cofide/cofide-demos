package main

import (
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
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

// authOutcome carries the identity/log fields for one successful (or
// attempted) authentication, for composeAuth to log uniformly across
// mechanisms.
type authOutcome struct {
	method string // "static-secret" | "mtls" | "jwt-svid" | "delegated-jwt"
	caller string // e.g. "bank-client" — for logging only
	fields []any  // extra slog key/value pairs, e.g. "spiffe_id", id.String()
}

// errNoCredential is returned by an authenticator when the request simply
// does not carry this mechanism's credential at all (no bearer token, no
// client cert) — as opposed to carrying an invalid one. composeAuth treats
// this distinctly for logging: trying the "other" mechanism is expected,
// routine behaviour in mixed mode, not a security event worth a warning.
var errNoCredential = errors.New("credential not presented")

// authenticator attempts one authentication mechanism against a request. It
// returns errNoCredential if the request doesn't carry this mechanism's
// credential at all, or any other error if the credential was presented but
// invalid/unauthorized.
type authenticator func(r *http.Request) (authOutcome, error)

// composeAuth tries every configured authenticator in order and authorizes
// the request on the first success. It only responds 401 once every
// configured authenticator has failed (including a wrong/invalid credential,
// not just a missing one) — this is what lets a route accept, e.g., "static
// key OR jwt-svid" without one caller's mechanism ever blocking another's.
func composeAuth(route string, authenticators []authenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, auth := range authenticators {
			outcome, err := auth(r)
			if err == nil {
				slog.Info("Authorised request", append([]any{"route", route, "auth_method", outcome.method, "caller", outcome.caller}, outcome.fields...)...)
				next(w, r)
				return
			}
			if !errors.Is(err, errNoCredential) {
				slog.Warn("Authentication attempt failed", "route", route, "auth_method", outcome.method, "caller", outcome.caller, "error", err)
			}
		}
		slog.Warn("Rejected request", "route", route, "reason", "no configured authentication mechanism succeeded")
		http.Error(w, "invalid or missing credentials", http.StatusUnauthorized)
	}
}

// verifyStaticKey is composeAuth's adapter for a pre-shared API key — the
// same check as staticAuthMiddleware, but returning an outcome instead of
// writing the HTTP response directly so it can be tried alongside other
// mechanisms for the same route in mixed mode.
func verifyStaticKey(caller, expectedKey string) authenticator {
	return func(r *http.Request) (authOutcome, error) {
		outcome := authOutcome{method: "static-secret", caller: caller}
		token, ok := bearerToken(r)
		if !ok {
			return outcome, errNoCredential
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			return outcome, errors.New("wrong API key")
		}
		return outcome, nil
	}
}

// verifyStaticAgentKey is verifyStaticKey plus logging bank-agent's asserted
// (not cryptographically verified) X-On-Behalf-Of header, mirroring
// staticAgentAuthMiddleware.
func verifyStaticAgentKey(expectedKey string) authenticator {
	return func(r *http.Request) (authOutcome, error) {
		outcome := authOutcome{method: "static-secret", caller: "bank-agent"}
		token, ok := bearerToken(r)
		if !ok {
			return outcome, errNoCredential
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			return outcome, errors.New("wrong API key")
		}
		onBehalfOf := r.Header.Get("X-On-Behalf-Of")
		if onBehalfOf == "" {
			onBehalfOf = "unknown"
		}
		outcome.fields = []any{"on_behalf_of_asserted_unverified", onBehalfOf}
		return outcome, nil
	}
}

// verifyJWTSVID is composeAuth's adapter for a JWT-SVID scoped to the given
// audience and issued to the given SPIFFE ID, validated against the local
// SPIFFE Workload API. caller names the workload this route expects the
// SVID to belong to, purely for logging.
func verifyJWTSVID(wlClient *workloadapi.Client, caller, audience string, authorizedID spiffeid.ID) authenticator {
	matcher := spiffeid.MatchID(authorizedID)
	return func(r *http.Request) (authOutcome, error) {
		outcome := authOutcome{method: "jwt-svid", caller: caller}
		token, ok := bearerToken(r)
		if !ok {
			return outcome, errNoCredential
		}

		svid, err := wlClient.ValidateJWTSVID(r.Context(), token, audience)
		if err != nil {
			return outcome, fmt.Errorf("invalid JWT-SVID: %w", err)
		}
		outcome.fields = []any{"spiffe_id", svid.ID.String()}

		if err := matcher(svid.ID); err != nil {
			return outcome, fmt.Errorf("unauthorized SPIFFE ID %s: %w", svid.ID, err)
		}

		return outcome, nil
	}
}

// verifyMTLS is composeAuth's adapter for a SPIFFE X.509-SVID presented as a
// TLS client certificate. It does no cryptographic re-verification: the
// certificate was already verified and authorized against expectedID inside
// the listener's tls.Config.VerifyPeerCertificate callback during the TLS
// handshake itself (see runMixed) — reaching this authenticator at all means
// it already passed. This only extracts the peer's SPIFFE ID for logging.
func verifyMTLS(caller string) authenticator {
	return func(r *http.Request) (authOutcome, error) {
		outcome := authOutcome{method: "mtls", caller: caller}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return outcome, errNoCredential
		}
		id, err := x509svid.IDFromCert(r.TLS.PeerCertificates[0])
		if err != nil {
			return outcome, fmt.Errorf("failed to extract SPIFFE ID from peer certificate: %w", err)
		}
		outcome.fields = []any{"spiffe_id", id.String()}
		return outcome, nil
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

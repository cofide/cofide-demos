package main

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
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
func staticAuthMiddleware(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// staticAgentAuthMiddleware authorises bank-agent's requests bearing a
// pre-shared API key, the same as staticAuthMiddleware, but also logs the
// caller's identity from the X-On-Behalf-Of header. Unlike the delegated
// JWT bank-agent presents in spiffe mode, this header is asserted by
// bank-agent, not cryptographically verified — that distinction is the
// point of the static/spiffe toggle.
func staticAgentAuthMiddleware(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			http.Error(w, "invalid or missing API key", http.StatusUnauthorized)
			return
		}
		onBehalfOf := r.Header.Get("X-On-Behalf-Of")
		if onBehalfOf == "" {
			onBehalfOf = "unknown"
		}
		slog.Info("Authorised bank-agent request", "on_behalf_of_asserted", onBehalfOf)
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
			http.Error(w, "no token provided", http.StatusUnauthorized)
			return
		}

		svid, err := wlClient.ValidateJWTSVID(r.Context(), token, audience)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid token: %s", err), http.StatusUnauthorized)
			return
		}

		if err := matcher(svid.ID); err != nil {
			http.Error(w, "unauthorized SPIFFE ID", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

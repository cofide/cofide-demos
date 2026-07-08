package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
)

const (
	stateCookieName = "bank_oauth_state"
	pkceCookieName  = "bank_pkce_verifier"
)

// handleLogin redirects the browser to the OIDC IdP to start the
// Authorization Code flow with PKCE. This is independent of AUTH_MODE —
// signing in as a customer is orthogonal to the static/SPIFFE toggle for
// the bank-client->bank-server and bank-agent->bank-server hops.
//
// bank-client is registered as a public OAuth2 client (no client_secret):
// the PKCE code_verifier/code_challenge pair generated here replaces the
// secret as proof that the /callback request came from whoever started
// this login, without bank-client having to hold a long-lived credential —
// exactly the kind of static secret this demo is otherwise toggling away
// from everywhere else.
func handleLogin(discovery *oidcDiscovery, clientID, redirectURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := randomToken(24)
		if err != nil {
			slog.Error("Failed to generate OAuth state", "error", err)
			http.Error(w, "Unable to start sign-in", http.StatusInternalServerError)
			return
		}
		codeVerifier, err := randomToken(32)
		if err != nil {
			slog.Error("Failed to generate PKCE code verifier", "error", err)
			http.Error(w, "Unable to start sign-in", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     pkceCookieName,
			Value:    codeVerifier,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   300,
		})

		authURL := discovery.authorizationURL(clientID, redirectURL, state, pkceChallenge(codeVerifier))
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// handleCallback completes the Authorization Code flow: verifies the state
// parameter, exchanges the code for tokens using the PKCE code_verifier
// (instead of a client_secret), and sets the session cookie.
func handleCallback(discovery *oidcDiscovery, httpClient *http.Client, clientID, redirectURL string, store *sessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stateCookie, err := r.Cookie(stateCookieName)
		if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
			http.Error(w, "Invalid or missing OAuth state", http.StatusBadRequest)
			return
		}
		verifierCookie, err := r.Cookie(pkceCookieName)
		if err != nil || verifierCookie.Value == "" {
			http.Error(w, "Missing PKCE code verifier", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		}

		tok, err := discovery.exchangeCode(httpClient, clientID, redirectURL, code, verifierCookie.Value)
		if err != nil {
			slog.Error("OIDC token exchange failed", "error", err)
			http.Error(w, "Sign-in failed", http.StatusBadGateway)
			return
		}
		clearCookie(w, stateCookieName)
		clearCookie(w, pkceCookieName)

		sess := session{
			Subject:     decodeUnverifiedClaim(tok.IDToken, "sub"),
			Name:        decodeUnverifiedClaim(tok.IDToken, "name"),
			AccessToken: tok.AccessToken,
		}
		if sess.Name == "" {
			sess.Name = decodeUnverifiedClaim(tok.IDToken, "email")
		}
		if err := store.setCookie(w, sess); err != nil {
			slog.Error("Failed to set session cookie", "error", err)
			http.Error(w, "Sign-in failed", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// handleLogout clears the session cookie.
func handleLogout(store *sessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store.clearCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
}

// randomToken returns a cryptographically random, URL-safe string encoding
// nBytes of entropy. Used for both the CSRF state parameter and the PKCE
// code_verifier (RFC 7636 requires 43-128 characters — nBytes=32 yields 43).
func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 code_challenge from a code_verifier per RFC 7636.
func pkceChallenge(codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

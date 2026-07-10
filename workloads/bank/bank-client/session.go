package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const sessionCookieName = "bank_session"

// session holds what bank-client needs about the signed-in user: enough to
// display who's logged in, and the OIDC ID token to forward as the caller's
// identity when proxying chat questions to bank-agent. The ID token, not the
// access token, is forwarded: an access token's "aud" is only ever set if
// bank-client explicitly requests a resource/audience during sign-in (it
// doesn't), so Ory issues it with an empty "aud" — useless for Credex's RFC
// 8693 subject-token audience check. An ID token's "aud" is mandatory per the
// OIDC spec and always equals the client ID, which is also what AgentCore's
// own inbound authorizer checks via allowed_clients (see agentcore.tf).
type session struct {
	Subject string `json:"sub"`
	Name    string `json:"name"`
	IDToken string `json:"idToken"`
}

// sessionStore signs and verifies session cookies with an HMAC, so the
// session data can live entirely in the cookie — no server-side store,
// consistent with the rest of this demo's in-memory ethos.
type sessionStore struct {
	secret []byte
}

func (s *sessionStore) setCookie(w http.ResponseWriter, sess session) error {
	payload, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := s.sign(encoded)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    encoded + "." + sig,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((8 * time.Hour).Seconds()),
	})
	return nil
}

func (s *sessionStore) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *sessionStore) fromRequest(r *http.Request) (*session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("not signed in")
	}

	encoded, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return nil, fmt.Errorf("malformed session cookie")
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(encoded))) != 1 {
		return nil, fmt.Errorf("invalid session signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("malformed session payload: %w", err)
	}
	var sess session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, fmt.Errorf("malformed session payload: %w", err)
	}
	return &sess, nil
}

func (s *sessionStore) sign(encoded string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	return hex.EncodeToString(mac.Sum(nil))
}

// decodeUnverifiedClaim reads a claim from a JWT's payload without verifying
// its signature. Safe here because the token was just received directly
// from the IdP's token endpoint over TLS (see exchangeCode), not from a
// redirect or another untrusted party — used only to populate the
// dashboard's display name, not for authorization decisions.
func decodeUnverifiedClaim(token, claim string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	v, _ := claims[claim].(string)
	return v
}

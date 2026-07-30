package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

func noopHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// logCapture is a slog.Handler test double that records every log record's
// message and attributes, for tests that need to assert on composeAuth's
// logging behaviour rather than just the final HTTP status.
type logCapture struct {
	mu      sync.Mutex
	records []string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	c.mu.Lock()
	c.records = append(c.records, b.String())
	c.mu.Unlock()
	return nil
}
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) contains(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

func withCapturedLogs(t *testing.T) *logCapture {
	t.Helper()
	prev := slog.Default()
	capture := &logCapture{}
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return capture
}

// --- (a) static bearer key ---

func TestBuildMuxes_StaticKey(t *testing.T) {
	env := &Env{StaticClientAPIKey: "test-client-key"}
	clientMux, _, _, _, err := buildMuxes(env, mixedDeps{}, noopHandler, noopHandler, noopHandler)
	if err != nil {
		t.Fatalf("buildMuxes: %v", err)
	}

	ts := httptest.NewServer(clientMux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer test-client-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid key: got status %d, want 200", resp.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req2.Header.Set("Authorization", "Bearer wrong-key")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key: got status %d, want 401", resp2.StatusCode)
	}
}

// --- (b) SPIFFE mTLS, fully offline via a hand-built trust bundle ---

// genSelfSignedCA generates a self-signed CA certificate/key.
func genSelfSignedCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return cert, key
}

// genSVIDCert generates a leaf certificate with the given SPIFFE ID as its
// sole URI SAN, signed by the given CA.
func genSVIDCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, spiffeID string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	uri, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatalf("parse spiffe ID: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// genServerCert generates a self-signed server certificate for 127.0.0.1 —
// the test client trusts it via InsecureSkipVerify, not chain verification,
// since this test exercises client-cert (mTLS) verification, not server
// authenticity.
func genServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "bank-server-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"127.0.0.1", "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestBuildMuxes_MTLS(t *testing.T) {
	const trustDomain = "example.org"
	const clientSPIFFEID = "spiffe://example.org/bank/client"

	caCert, caKey := genSelfSignedCA(t)
	bundle := x509bundle.FromX509Authorities(spiffeid.RequireTrustDomainFromString(trustDomain), []*x509.Certificate{caCert})
	serverCert := genServerCert(t)

	env := &Env{ClientSPIFFEID: clientSPIFFEID}
	deps := mixedDeps{
		bundleSource:   bundle,
		getCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &serverCert, nil },
	}
	clientMux, _, clientTLSConfig, _, err := buildMuxes(env, deps, noopHandler, noopHandler, noopHandler)
	if err != nil {
		t.Fatalf("buildMuxes: %v", err)
	}

	ts := httptest.NewUnstartedServer(clientMux)
	ts.TLS = clientTLSConfig
	ts.StartTLS()
	defer ts.Close()

	get := func(clientCert *tls.Certificate) (*http.Response, error) {
		tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test client, verifying server identity isn't the point of this test
		if clientCert != nil {
			tlsConfig.Certificates = []tls.Certificate{*clientCert}
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
		return client.Get(ts.URL + "/api/summary")
	}

	authorizedCert := genSVIDCert(t, caCert, caKey, clientSPIFFEID)
	resp, err := get(&authorizedCert)
	if err != nil {
		t.Fatalf("authorized cert: request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized cert: got status %d, want 200", resp.StatusCode)
	}

	wrongCert := genSVIDCert(t, caCert, caKey, "spiffe://example.org/bank/someone-else")
	if _, err := get(&wrongCert); err == nil {
		t.Fatal("wrong SPIFFE ID: expected TLS handshake to fail, it succeeded")
	}

	// No client cert presented at all: the TLS handshake succeeds (a cert is
	// requested, not required), but with no static key configured either,
	// composeAuth has no authenticator left to try and rejects at the HTTP
	// layer with 401 — not a transport-level failure.
	noCertResp, err := get(nil)
	if err != nil {
		t.Fatalf("no client cert: request failed at transport level: %v", err)
	}
	defer func() { _ = noCertResp.Body.Close() }()
	if noCertResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no client cert and no static key configured: got status %d, want 401", noCertResp.StatusCode)
	}
}

func TestBuildMuxes_MTLS_FallsBackToStaticKey(t *testing.T) {
	const trustDomain = "example.org"
	const clientSPIFFEID = "spiffe://example.org/bank/client"

	caCert, _ := genSelfSignedCA(t)
	bundle := x509bundle.FromX509Authorities(spiffeid.RequireTrustDomainFromString(trustDomain), []*x509.Certificate{caCert})
	serverCert := genServerCert(t)

	env := &Env{ClientSPIFFEID: clientSPIFFEID, StaticClientAPIKey: "test-client-key"}
	deps := mixedDeps{
		bundleSource:   bundle,
		getCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &serverCert, nil },
	}
	clientMux, _, clientTLSConfig, _, err := buildMuxes(env, deps, noopHandler, noopHandler, noopHandler)
	if err != nil {
		t.Fatalf("buildMuxes: %v", err)
	}

	ts := httptest.NewUnstartedServer(clientMux)
	ts.TLS = clientTLSConfig
	ts.StartTLS()
	defer ts.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer test-client-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("no client cert, static key present: request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no client cert, static key present: got status %d, want 200", resp.StatusCode)
	}
}

// --- (d) Credex delegated JWT, fully fakeable offline ---

func newTestJWKS(t *testing.T) (*rsa.PrivateKey, jose.JSONWebKeySet) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       &key.PublicKey,
		KeyID:     "test-key",
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}}}
	return key, jwks
}

func newCredexTestServer(t *testing.T, jwks jose.JSONWebKeySet) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var jwksURI string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": jwksURI})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})
	ts := httptest.NewServer(mux)
	jwksURI = ts.URL + "/jwks"
	t.Cleanup(ts.Close)
	return ts
}

func signDelegatedToken(t *testing.T, key *rsa.PrivateKey, subject, actor string, audience []string) string {
	t.Helper()
	signingKey := jose.JSONWebKey{Key: key, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	claims := delegatedClaims{
		Claims: jwt.Claims{
			Subject:  subject,
			Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Audience: audience,
		},
		Act: &actorClaim{Sub: actor},
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return token
}

func TestBuildMuxes_DelegatedJWT(t *testing.T) {
	const authorizedActor = "arn:aws:iam::123456789012:role/bank-agent"
	capture := withCapturedLogs(t)

	key, jwks := newTestJWKS(t)
	credex := newCredexTestServer(t, jwks)

	env := &Env{
		AgentAuthorizedActor: authorizedActor,
		CredexDiscoveryURL:   credex.URL + "/.well-known/openid-configuration",
		AgentTokenAudience:   "bank-server-agent-api",
	}
	_, webhookMux, _, _, err := buildMuxes(env, mixedDeps{}, noopHandler, noopHandler, noopHandler)
	if err != nil {
		t.Fatalf("buildMuxes: %v", err)
	}

	ts := httptest.NewServer(webhookMux)
	defer ts.Close()

	token := signDelegatedToken(t, key, "customer-1", authorizedActor, nil)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	if !capture.contains("on_behalf_of_verified=customer-1") || !capture.contains("actor="+authorizedActor) {
		t.Errorf("expected log fields for verified on-behalf-of customer and actor, got: %v", capture.records)
	}

	wrongActorToken := signDelegatedToken(t, key, "customer-1", "arn:aws:iam::123456789012:role/someone-else", nil)
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req2.Header.Set("Authorization", "Bearer "+wrongActorToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong actor: got status %d, want 401", resp2.StatusCode)
	}
}

// --- (e) composition semantics: reject only once every configured mechanism fails ---

// TestComposeAuth_TriesAllBeforeRejecting exercises the bank-agent route
// (static key OR delegated JWT), not bank-lambda's (static key OR
// jwt-svid): jwt-svid validation always round-trips to a live Workload API
// (see verifyJWTSVID), so it has no safe offline path — calling it with a
// nil wlClient panics rather than cleanly failing. Delegated-JWT validation
// only needs a JWKS endpoint, which is trivially fakeable, so it's the
// mechanism pairing that can actually prove composeAuth's contract offline.
func TestComposeAuth_TriesAllBeforeRejecting(t *testing.T) {
	capture := withCapturedLogs(t)

	_, jwks := newTestJWKS(t)
	credex := newCredexTestServer(t, jwks)

	env := &Env{
		StaticAgentAPIKey:    "test-agent-key",
		AgentAuthorizedActor: "arn:aws:iam::123456789012:role/bank-agent",
		CredexDiscoveryURL:   credex.URL + "/.well-known/openid-configuration",
		AgentTokenAudience:   "bank-server-agent-api",
	}
	_, webhookMux, _, _, err := buildMuxes(env, mixedDeps{}, noopHandler, noopHandler, noopHandler)
	if err != nil {
		t.Fatalf("buildMuxes: %v", err)
	}

	ts := httptest.NewServer(webhookMux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer garbage-credential-matching-neither")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", resp.StatusCode)
	}

	if !capture.contains("auth_method=static-secret") {
		t.Errorf("expected the static-secret authenticator to have been attempted, logs: %v", capture.records)
	}
	if !capture.contains("auth_method=delegated-jwt") {
		t.Errorf("expected the delegated-jwt authenticator to have been attempted, logs: %v", capture.records)
	}
	if !capture.contains("no configured authentication mechanism succeeded") {
		t.Errorf("expected final rejection log, logs: %v", capture.records)
	}
}

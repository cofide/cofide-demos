package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	authModeStatic = "static"
	// authModeMixed serves both listeners over webPKI TLS and, on every
	// route, accepts either a pre-shared static API key or the equivalent
	// SPIFFE-based credential — whichever the caller currently presents.
	// This supersedes the old SPIFFE-only mode: once a bank-server
	// deployment is in mixed mode, individual callers can switch between
	// static and SPIFFE independently, with no further bank-server restart.
	authModeMixed = "mixed"

	webhookAudience    = "bank-server-webhook"
	fraudCheckAudience = "bank-server-fraud-check"

	// tlsReloadInterval controls how often the mixed-mode TLS certificate is
	// re-read from disk, so cert-manager's periodic rotation (which replaces
	// the mounted Secret's files via an atomic symlink swap) is picked up
	// without a pod restart. Polling, not fsnotify: a watch on the leaf file
	// itself is silently invalidated the moment the swap happens.
	tlsReloadInterval = 60 * time.Second
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Error running bank-server", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	AuthMode         string
	ClientAPIAddress string
	WebhookAddress   string

	// Mixed mode only, both required.
	TLSCertFile string
	TLSKeyFile  string

	// Mixed mode only; only connected to if at least one of
	// ClientSPIFFEID/LambdaSPIFFEID/FraudCheckerSPIFFEID below is set.
	SpiffeSocketPath string

	// Each pair below is "at least one of the two must be set" for these
	// three mandatory routes in mixed mode (enforced in getEnv via
	// requireOneOf) — plain optional strings, empty means "this mechanism
	// disabled for this route", not gated by AuthMode beyond that.
	ClientSPIFFEID         string
	StaticClientAPIKey     string
	LambdaSPIFFEID         string
	StaticWebhookAPIKey    string
	FraudCheckerSPIFFEID   string
	StaticFraudCheckAPIKey string

	// bank-agent route: fully optional, as today — registered iff at least
	// one of {StaticAgentAPIKey, (AgentAuthorizedActor && CredexDiscoveryURL)}
	// is configured.
	StaticAgentAPIKey    string
	AgentAuthorizedActor string
	CredexDiscoveryURL   string
	AgentTokenAudience   string
}

func mustGetEnv(variable string) string {
	v, ok := os.LookupEnv(variable)
	if !ok || v == "" {
		slog.Error("Unset environment variable", "variable", variable)
		os.Exit(1)
	}
	return v
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnv() *Env {
	authMode := getEnvWithDefault("AUTH_MODE", authModeStatic)

	env := &Env{
		AuthMode:         authMode,
		ClientAPIAddress: getEnvWithDefault("CLIENT_API_ADDRESS", ":8443"),
		WebhookAddress:   getEnvWithDefault("WEBHOOK_ADDRESS", ":8444"),
	}

	switch authMode {
	case authModeStatic:
		env.StaticClientAPIKey = mustGetEnv("STATIC_CLIENT_API_KEY")
		env.StaticWebhookAPIKey = mustGetEnv("STATIC_WEBHOOK_API_KEY")
		env.StaticFraudCheckAPIKey = mustGetEnv("STATIC_FRAUD_CHECK_API_KEY")
		// Optional, not mustGetEnv: bank-agent has its own separate
		// deployment bootstrap (see workloads/bank/README.md), so
		// bank-server must be able to start without it configured yet.
		env.StaticAgentAPIKey = getEnvWithDefault("STATIC_AGENT_API_KEY", "")
	case authModeMixed:
		env.TLSCertFile = getEnvWithDefault("TLS_CERT_FILE", "/etc/bank-server/tls/tls.crt")
		env.TLSKeyFile = getEnvWithDefault("TLS_KEY_FILE", "/etc/bank-server/tls/tls.key")

		env.ClientSPIFFEID = getEnvWithDefault("CLIENT_SPIFFE_ID", "")
		env.StaticClientAPIKey = getEnvWithDefault("STATIC_CLIENT_API_KEY", "")
		requireOneOf("bank-client", env.ClientSPIFFEID, env.StaticClientAPIKey)

		env.LambdaSPIFFEID = getEnvWithDefault("LAMBDA_SPIFFE_ID", "")
		env.StaticWebhookAPIKey = getEnvWithDefault("STATIC_WEBHOOK_API_KEY", "")
		requireOneOf("bank-lambda", env.LambdaSPIFFEID, env.StaticWebhookAPIKey)

		env.FraudCheckerSPIFFEID = getEnvWithDefault("FRAUD_CHECKER_SPIFFE_ID", "")
		env.StaticFraudCheckAPIKey = getEnvWithDefault("STATIC_FRAUD_CHECK_API_KEY", "")
		requireOneOf("bank-fraud-checker", env.FraudCheckerSPIFFEID, env.StaticFraudCheckAPIKey)

		// Optional: bank-agent has its own separate deployment bootstrap (see
		// workloads/bank/README.md), so bank-server must be able to start
		// before either its static key or its Credex delegated-JWT config is
		// set — it just disables the agent-facing route until at least one is.
		env.StaticAgentAPIKey = getEnvWithDefault("STATIC_AGENT_API_KEY", "")
		env.AgentAuthorizedActor = getEnvWithDefault("AGENT_AUTHORIZED_ACTOR", "")
		env.CredexDiscoveryURL = getEnvWithDefault("CREDEX_DISCOVERY_URL", "")
		env.AgentTokenAudience = getEnvWithDefault("AGENT_TOKEN_AUDIENCE", "bank-server-agent-api")
		if (env.AgentAuthorizedActor == "") != (env.CredexDiscoveryURL == "") {
			slog.Error("AGENT_AUTHORIZED_ACTOR and CREDEX_DISCOVERY_URL must be set together")
			os.Exit(1)
		}

		if env.ClientSPIFFEID != "" || env.LambdaSPIFFEID != "" || env.FraudCheckerSPIFFEID != "" {
			env.SpiffeSocketPath = getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock")
		}
	default:
		slog.Error("Invalid AUTH_MODE", "value", authMode)
		os.Exit(1)
	}

	return env
}

// requireOneOf fails startup if neither a SPIFFE ID nor a static API key is
// configured for a mandatory route — in mixed mode, a route must have at
// least one working authentication mechanism.
func requireOneOf(route, spiffeID, staticKey string) {
	if spiffeID == "" && staticKey == "" {
		slog.Error("Route has no configured authentication mechanism", "route", route)
		os.Exit(1)
	}
}

func run(ctx context.Context, env *Env) error {
	ledger := newLedger()

	summaryHandler := handleSummary(ledger)
	webhookHandler := handleWebhook(ledger)
	fraudCheckHandler := handleFraudCheck(ledger)

	switch env.AuthMode {
	case authModeStatic:
		return runStatic(env, summaryHandler, webhookHandler, fraudCheckHandler)
	case authModeMixed:
		return runMixed(ctx, env, summaryHandler, webhookHandler, fraudCheckHandler)
	default:
		return fmt.Errorf("invalid AUTH_MODE: %s", env.AuthMode)
	}
}

// runStatic serves both surfaces over plain HTTP, authorising requests with a
// pre-shared API key — the "before Cofide Connect" story. bank-lambda and
// bank-agent share a single external-facing listener: both are already
// plain-HTTP bearer-token surfaces (unlike the client listener's mTLS), just
// on different routes with different auth middleware — sharing one address
// means only one port to expose from AWS (one NodePort/tunnel entry),
// instead of one per AWS-hosted caller.
func runStatic(env *Env, summaryHandler, webhookHandler, fraudCheckHandler http.HandlerFunc) error {
	clientMux := http.NewServeMux()
	clientMux.HandleFunc("/api/summary", staticAuthMiddleware("bank-client", env.StaticClientAPIKey, summaryHandler))

	externalMux := http.NewServeMux()
	externalMux.HandleFunc("/webhook/transactions", staticAuthMiddleware("bank-lambda", env.StaticWebhookAPIKey, webhookHandler))
	externalMux.HandleFunc("/api/fraud-check", staticAuthMiddleware("bank-fraud-checker", env.StaticFraudCheckAPIKey, fraudCheckHandler))

	// bank-agent has its own separate deployment bootstrap (see
	// workloads/bank/README.md) — don't register this route until it's
	// configured.
	if env.StaticAgentAPIKey != "" {
		externalMux.HandleFunc("/api/summary", staticAgentAuthMiddleware(env.StaticAgentAPIKey, summaryHandler))
	} else {
		slog.Info("STATIC_AGENT_API_KEY not set — bank-agent's API is disabled")
	}

	listeners := []namedServer{
		{"Client API server (static API key)", env.ClientAPIAddress, httpServer(env.ClientAPIAddress, clientMux).ListenAndServe},
		{"External API server (static API key)", env.WebhookAddress, httpServer(env.WebhookAddress, externalMux).ListenAndServe},
	}

	return runListeners(listeners)
}

// mixedDeps bundles runMixed's external dependencies so buildMuxes can be
// tested without a real Workload API socket or real mounted cert files.
type mixedDeps struct {
	bundleSource   x509bundle.Source
	wlClient       *workloadapi.Client
	getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// runMixed serves both listeners over webPKI TLS (a cert-manager-issued
// certificate, reloaded periodically from disk — see certReloader) and, on
// every route, accepts either a pre-shared static API key or the caller's
// SPIFFE-based credential (X.509-SVID mTLS for bank-client; a JWT-SVID for
// bank-lambda/bank-fraud-checker; a Credex-minted delegated JWT for
// bank-agent) — whichever it currently presents. Once a bank-server
// deployment is running this mode, individual callers can switch between
// static and SPIFFE independently, with no further bank-server restart.
func runMixed(ctx context.Context, env *Env, summaryHandler, webhookHandler, fraudCheckHandler http.HandlerFunc) error {
	certReloader, err := newCertReloader(env.TLSCertFile, env.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}
	go certReloader.watch(ctx, tlsReloadInterval)

	deps := mixedDeps{getCertificate: certReloader.GetCertificate}

	// The trust bundle and workload API client are only needed if at least
	// one caller is currently using a SPIFFE-based credential — a
	// mixed-mode deployment where every caller still presents a static key
	// has no need to talk to the Workload API at all.
	if env.ClientSPIFFEID != "" || env.LambdaSPIFFEID != "" || env.FraudCheckerSPIFFEID != "" {
		bundleSource, err := workloadapi.NewBundleSource(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
		if err != nil {
			return fmt.Errorf("unable to obtain SPIFFE trust bundle: %w", err)
		}
		defer func() { _ = bundleSource.Close() }()
		deps.bundleSource = bundleSource

		wlClient, err := workloadapi.New(ctx, workloadapi.WithAddr(env.SpiffeSocketPath))
		if err != nil {
			return fmt.Errorf("failed to create workload client: %w", err)
		}
		defer func() { _ = wlClient.Close() }()
		deps.wlClient = wlClient
	}

	clientMux, webhookMux, clientTLSConfig, webhookTLSConfig, err := buildMuxes(env, deps, summaryHandler, webhookHandler, fraudCheckHandler)
	if err != nil {
		return err
	}

	clientServer := httpServer(env.ClientAPIAddress, clientMux)
	clientServer.TLSConfig = clientTLSConfig
	webhookServer := httpServer(env.WebhookAddress, webhookMux)
	webhookServer.TLSConfig = webhookTLSConfig

	listeners := []namedServer{
		{"Client API server (TLS, static key and/or mTLS)", env.ClientAPIAddress, func() error { return clientServer.ListenAndServeTLS("", "") }},
		{"External API server (TLS, static key and/or JWT-SVID/delegated JWT)", env.WebhookAddress, func() error { return webhookServer.ListenAndServeTLS("", "") }},
	}
	return runListeners(listeners)
}

// buildMuxes wires up every route's authenticator(s) and both listeners'
// TLS configs from env and deps. Split out from runMixed as a pure,
// side-effect-free (beyond one Credex JWKS discovery HTTP call) function so
// tests can exercise it with fake deps instead of a real Workload API
// socket or mounted cert files.
func buildMuxes(env *Env, deps mixedDeps, summaryHandler, webhookHandler, fraudCheckHandler http.HandlerFunc) (clientMux, webhookMux *http.ServeMux, clientTLSConfig, webhookTLSConfig *tls.Config, err error) {
	// --- client listener: static key OR mTLS ---
	var clientAuthenticators []authenticator
	if env.StaticClientAPIKey != "" {
		clientAuthenticators = append(clientAuthenticators, verifyStaticKey("bank-client", env.StaticClientAPIKey))
	}
	clientTLSConfig = &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: deps.getCertificate,
	}
	if env.ClientSPIFFEID != "" {
		clientSPIFFEID, parseErr := spiffeid.FromString(env.ClientSPIFFEID)
		if parseErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to parse CLIENT_SPIFFE_ID: %w", parseErr)
		}
		clientAuthenticators = append(clientAuthenticators, verifyMTLS("bank-client"))

		bundleSource := deps.bundleSource
		authorizer := loggingAuthorizer("bank-client", tlsconfig.AuthorizeOneOf(clientSPIFFEID))
		// Request, don't require, a client certificate: an unset cert
		// (rawCerts empty) is accepted here so composeAuth can still fall
		// through to a static key for callers not yet using mTLS; a
		// presented cert is fully verified against the SPIFFE trust bundle
		// and rejected at the handshake if it doesn't check out.
		clientTLSConfig.ClientAuth = tls.RequestClientCert
		clientTLSConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return nil
			}
			return tlsconfig.VerifyPeerCertificate(bundleSource, authorizer)(rawCerts, nil)
		}
	}
	clientMux = http.NewServeMux()
	clientMux.HandleFunc("/api/summary", composeAuth("bank-client", clientAuthenticators, summaryHandler))

	// --- webhook listener: static key OR jwt-svid, per route ---
	webhookMux = http.NewServeMux()

	var lambdaAuthenticators []authenticator
	if env.StaticWebhookAPIKey != "" {
		lambdaAuthenticators = append(lambdaAuthenticators, verifyStaticKey("bank-lambda", env.StaticWebhookAPIKey))
	}
	if env.LambdaSPIFFEID != "" {
		lambdaSPIFFEID, parseErr := spiffeid.FromString(env.LambdaSPIFFEID)
		if parseErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to parse LAMBDA_SPIFFE_ID: %w", parseErr)
		}
		lambdaAuthenticators = append(lambdaAuthenticators, verifyJWTSVID(deps.wlClient, "bank-lambda", webhookAudience, lambdaSPIFFEID))
	}
	webhookMux.HandleFunc("/webhook/transactions", composeAuth("bank-lambda", lambdaAuthenticators, webhookHandler))

	var fraudCheckAuthenticators []authenticator
	if env.StaticFraudCheckAPIKey != "" {
		fraudCheckAuthenticators = append(fraudCheckAuthenticators, verifyStaticKey("bank-fraud-checker", env.StaticFraudCheckAPIKey))
	}
	if env.FraudCheckerSPIFFEID != "" {
		fraudCheckerSPIFFEID, parseErr := spiffeid.FromString(env.FraudCheckerSPIFFEID)
		if parseErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to parse FRAUD_CHECKER_SPIFFE_ID: %w", parseErr)
		}
		fraudCheckAuthenticators = append(fraudCheckAuthenticators, verifyJWTSVID(deps.wlClient, "bank-fraud-checker", fraudCheckAudience, fraudCheckerSPIFFEID))
	}
	webhookMux.HandleFunc("/api/fraud-check", composeAuth("bank-fraud-checker", fraudCheckAuthenticators, fraudCheckHandler))

	// --- bank-agent route: fully optional, static key OR delegated JWT ---
	var agentAuthenticators []authenticator
	if env.StaticAgentAPIKey != "" {
		agentAuthenticators = append(agentAuthenticators, verifyStaticAgentKey(env.StaticAgentAPIKey))
	}
	if env.AgentAuthorizedActor != "" && env.CredexDiscoveryURL != "" {
		// Custom User-Agent: Credex's OIDC discovery/JWKS endpoints in this
		// demo are fronted by a Cloudflare tunnel, which blocks Go's default
		// "Go-http-client/1.1" User-Agent (masked as a 404, not a 403) — the
		// same issue already hit and fixed for bank-lambda's Python client.
		httpClient := &http.Client{Timeout: 10 * time.Second, Transport: &userAgentTransport{userAgent: "cofide-bank-server/1.0"}}
		slog.Info("Discovering Credex JWKS endpoint", "issuer", env.CredexDiscoveryURL)
		jwksURI, discErr := discoverJWKSURI(env.CredexDiscoveryURL, httpClient)
		if discErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to discover Credex JWKS endpoint: %w", discErr)
		}
		agentAuthenticators = append(agentAuthenticators, verifyDelegatedJWT(&JWKSFetcher{url: jwksURI, client: httpClient}, env.AgentTokenAudience, env.AgentAuthorizedActor))
	}
	if len(agentAuthenticators) > 0 {
		webhookMux.HandleFunc("/api/summary", composeAuth("bank-agent", agentAuthenticators, summaryHandler))
	} else {
		slog.Info("No bank-agent credentials configured — bank-agent's API is disabled")
	}

	webhookTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: deps.getCertificate}

	return clientMux, webhookMux, clientTLSConfig, webhookTLSConfig, nil
}

// namedServer pairs a human-readable label and address (for logging) with
// the blocking function that serves it.
type namedServer struct {
	name string
	addr string
	run  func() error
}

// runListeners starts every listener concurrently and returns as soon as
// any one of them exits.
func runListeners(listeners []namedServer) error {
	errCh := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() {
			slog.Info(l.name+" starting", "address", l.addr)
			errCh <- l.run()
		}()
	}
	return fmt.Errorf("server exited: %w", <-errCh)
}

func httpServer(addr string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func handleSummary(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(ledger.Summary()); err != nil {
			slog.Error("Error encoding summary", "error", err)
		}
	}
}

func handleWebhook(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var txn Transaction
		if err := json.NewDecoder(r.Body).Decode(&txn); err != nil {
			http.Error(w, "invalid transaction payload", http.StatusBadRequest)
			return
		}
		if txn.Merchant == "" || txn.AmountPence == 0 {
			http.Error(w, "merchant and amountPence are required", http.StatusBadRequest)
			return
		}

		ledger.AddTransaction(txn)
		slog.Info("Recorded transaction", "merchant", txn.Merchant, "amountPence", txn.AmountPence)
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleFraudCheck serves bank-fraud-checker's two-step poll: GET lists the
// current transactions (so it can log what's outstanding before acting), POST
// marks every currently-unchecked transaction as checked. Both live under one
// route/one auth middleware instance, since it's the same caller either way —
// bank-server has no separate "list-only" credential for this workload.
// Callers don't select specific transactions to check — each POST simply
// clears whatever's pending.
func handleFraudCheck(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(ledger.Summary()); err != nil {
				slog.Error("Error encoding summary", "error", err)
			}
		case http.MethodPost:
			count := ledger.MarkFraudChecked(time.Now())
			slog.Info("Marked transactions as fraud-checked", "count", count)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			if err := json.NewEncoder(w).Encode(map[string]int{"checked": count}); err != nil {
				slog.Error("Error encoding fraud-check response", "error", err)
			}
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

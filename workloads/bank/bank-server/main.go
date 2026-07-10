package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	authModeStatic = "static"
	authModeSPIFFE = "spiffe"

	webhookAudience = "bank-server-webhook"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Error running bank-server", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	AuthMode             string
	ClientAPIAddress     string
	WebhookAddress       string
	SpiffeSocketPath     string
	ClientSPIFFEID       string
	LambdaSPIFFEID       string
	AgentAuthorizedActor string
	StaticClientAPIKey   string
	StaticWebhookAPIKey  string
	StaticAgentAPIKey    string
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
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
	}

	switch authMode {
	case authModeStatic:
		env.StaticClientAPIKey = mustGetEnv("STATIC_CLIENT_API_KEY")
		env.StaticWebhookAPIKey = mustGetEnv("STATIC_WEBHOOK_API_KEY")
		// Optional, not mustGetEnv: bank-agent has its own separate
		// deployment bootstrap (see workloads/bank/README.md), so
		// bank-server must be able to start without it configured yet.
		env.StaticAgentAPIKey = getEnvWithDefault("STATIC_AGENT_API_KEY", "")
	case authModeSPIFFE:
		env.ClientSPIFFEID = mustGetEnv("CLIENT_SPIFFE_ID")
		env.LambdaSPIFFEID = mustGetEnv("LAMBDA_SPIFFE_ID")
		env.AgentAuthorizedActor = getEnvWithDefault("AGENT_AUTHORIZED_ACTOR", "")
		env.CredexDiscoveryURL = getEnvWithDefault("CREDEX_DISCOVERY_URL", "")
		env.AgentTokenAudience = getEnvWithDefault("AGENT_TOKEN_AUDIENCE", "bank-server-agent-api")
	default:
		slog.Error("Invalid AUTH_MODE", "value", authMode)
		os.Exit(1)
	}

	return env
}

func run(ctx context.Context, env *Env) error {
	ledger := newLedger()

	summaryHandler := handleSummary(ledger)
	webhookHandler := handleWebhook(ledger)

	switch env.AuthMode {
	case authModeStatic:
		return runStatic(env, summaryHandler, webhookHandler)
	case authModeSPIFFE:
		return runSPIFFE(ctx, env, summaryHandler, webhookHandler)
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
func runStatic(env *Env, summaryHandler, webhookHandler http.HandlerFunc) error {
	clientMux := http.NewServeMux()
	clientMux.HandleFunc("/api/summary", staticAuthMiddleware("bank-client", env.StaticClientAPIKey, summaryHandler))

	externalMux := http.NewServeMux()
	externalMux.HandleFunc("/webhook/transactions", staticAuthMiddleware("bank-lambda", env.StaticWebhookAPIKey, webhookHandler))

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

// runSPIFFE serves the client-facing summary API over SPIFFE X.509-SVID
// mTLS, and the Lambda-facing webhook and agent-facing summary API — both
// plain HTTP, authorised by a self-verifying bearer JWT (a SPIFFE JWT-SVID
// for the Lambda, a Credex-minted delegated token for the agent), so
// neither requires TLS termination — on a single shared external listener.
// Neither can share the client listener's mTLS: bank-lambda and bank-agent
// (an AgentCore Runtime workload) have no SPIFFE Workload API socket to
// obtain an X.509-SVID from, so their identity is presented as a bearer
// token instead. They *can* share a listener with each other, though —
// different routes, different auth middleware, one port to expose from AWS.
func runSPIFFE(ctx context.Context, env *Env, summaryHandler, webhookHandler http.HandlerFunc) error {
	slog.Info("Waiting for X.509 SVID")
	x509Source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
	if err != nil {
		return fmt.Errorf("unable to obtain X.509 SVID: %w", err)
	}
	defer func() { _ = x509Source.Close() }()
	slog.Info("Retrieved X.509 SVID")

	wlClient, err := workloadapi.New(ctx, workloadapi.WithAddr(env.SpiffeSocketPath))
	if err != nil {
		return fmt.Errorf("failed to create workload client: %w", err)
	}
	defer func() { _ = wlClient.Close() }()

	clientSPIFFEID, err := spiffeid.FromString(env.ClientSPIFFEID)
	if err != nil {
		return fmt.Errorf("failed to parse CLIENT_SPIFFE_ID: %w", err)
	}
	lambdaSPIFFEID, err := spiffeid.FromString(env.LambdaSPIFFEID)
	if err != nil {
		return fmt.Errorf("failed to parse LAMBDA_SPIFFE_ID: %w", err)
	}

	externalMux := http.NewServeMux()
	externalMux.HandleFunc("/webhook/transactions", jwtSVIDAuthMiddleware(wlClient, webhookAudience, lambdaSPIFFEID, webhookHandler))

	clientMux := http.NewServeMux()
	clientMux.HandleFunc("/api/summary", summaryHandler)
	tlsConfig := tlsconfig.MTLSServerConfig(x509Source, x509Source, loggingAuthorizer("bank-client", tlsconfig.AuthorizeOneOf(clientSPIFFEID)))
	clientServer := httpServer(env.ClientAPIAddress, clientMux)
	clientServer.TLSConfig = tlsConfig

	// bank-agent has its own separate deployment bootstrap (see
	// workloads/bank/README.md) — don't register this route, or discover
	// Credex's JWKS endpoint, until it's configured.
	if env.AgentAuthorizedActor != "" && env.CredexDiscoveryURL != "" {
		httpClient := &http.Client{Timeout: 10 * time.Second}
		slog.Info("Discovering Credex JWKS endpoint", "issuer", env.CredexDiscoveryURL)
		jwksURI, err := discoverJWKSURI(env.CredexDiscoveryURL, httpClient)
		if err != nil {
			return fmt.Errorf("failed to discover Credex JWKS endpoint: %w", err)
		}
		jwksFetcher := &JWKSFetcher{url: jwksURI, client: httpClient}

		externalMux.HandleFunc("/api/summary", delegatedJWTAuthMiddleware(jwksFetcher, env.AgentTokenAudience, env.AgentAuthorizedActor, summaryHandler))
	} else {
		slog.Info("AGENT_AUTHORIZED_ACTOR/CREDEX_DISCOVERY_URL not set — bank-agent's API is disabled")
	}

	listeners := []namedServer{
		{"External API server (JWT-SVID / delegated JWT)", env.WebhookAddress, httpServer(env.WebhookAddress, externalMux).ListenAndServe},
		{"Client API server (mTLS)", env.ClientAPIAddress, func() error { return clientServer.ListenAndServeTLS("", "") }},
	}

	return runListeners(listeners)
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

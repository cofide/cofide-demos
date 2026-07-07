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
	AuthMode            string
	ClientAPIAddress    string
	WebhookAddress      string
	SpiffeSocketPath    string
	ClientSPIFFEID      string
	LambdaSPIFFEID      string
	StaticClientAPIKey  string
	StaticWebhookAPIKey string
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
	case authModeSPIFFE:
		env.ClientSPIFFEID = mustGetEnv("CLIENT_SPIFFE_ID")
		env.LambdaSPIFFEID = mustGetEnv("LAMBDA_SPIFFE_ID")
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
// pre-shared API key — the "before Cofide Connect" story.
func runStatic(env *Env, summaryHandler, webhookHandler http.HandlerFunc) error {
	clientMux := http.NewServeMux()
	clientMux.HandleFunc("/api/summary", staticAuthMiddleware(env.StaticClientAPIKey, summaryHandler))

	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("/webhook/transactions", staticAuthMiddleware(env.StaticWebhookAPIKey, webhookHandler))

	errCh := make(chan error, 2)
	go func() {
		slog.Info("Client API server starting (static API key)", "address", env.ClientAPIAddress)
		errCh <- httpServer(env.ClientAPIAddress, clientMux).ListenAndServe()
	}()
	go func() {
		slog.Info("Webhook server starting (static API key)", "address", env.WebhookAddress)
		errCh <- httpServer(env.WebhookAddress, webhookMux).ListenAndServe()
	}()

	return fmt.Errorf("server exited: %w", <-errCh)
}

// runSPIFFE serves the client-facing summary API over SPIFFE X.509-SVID mTLS,
// and the Lambda-facing webhook over plain HTTP authorised by a SPIFFE
// JWT-SVID (self-verifying, so no TLS termination is required) — the "after
// onboarding into Connect" story.
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

	clientMux := http.NewServeMux()
	clientMux.HandleFunc("/api/summary", summaryHandler)

	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("/webhook/transactions", jwtSVIDAuthMiddleware(wlClient, webhookAudience, lambdaSPIFFEID, webhookHandler))

	tlsConfig := tlsconfig.MTLSServerConfig(x509Source, x509Source, tlsconfig.AuthorizeOneOf(clientSPIFFEID))
	clientServer := httpServer(env.ClientAPIAddress, clientMux)
	clientServer.TLSConfig = tlsConfig

	errCh := make(chan error, 2)
	go func() {
		slog.Info("Webhook server starting (JWT-SVID)", "address", env.WebhookAddress)
		errCh <- httpServer(env.WebhookAddress, webhookMux).ListenAndServe()
	}()
	go func() {
		slog.Info("Client API server starting (mTLS)", "address", env.ClientAPIAddress)
		errCh <- clientServer.ListenAndServeTLS("", "")
	}()

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

package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	authModeStatic = "static"
	authModeSPIFFE = "spiffe"
)

//go:embed templates/dashboard.html.tmpl
var templatesFS embed.FS

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Error running bank-client", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	AuthMode           string
	ListenAddress      string
	BankServerHost     string
	BankServerPort     string
	SpiffeSocketPath   string
	ServerSPIFFEID     string
	StaticClientAPIKey string

	// OIDC sign-in and the bank-agent chat proxy are independent of AuthMode
	// — signing in as a customer is orthogonal to the static/SPIFFE toggle.
	// bank-client is a public OAuth2 client (PKCE, no client_secret) — see
	// login.go. Any OIDC-compliant IdP works here (Ory, Auth0, Okta, ...).
	OIDCDiscoveryURL   string
	OIDCClientID       string
	OIDCRedirectURL    string
	BankAgentInvokeURL string
	SessionSecret      string
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
		ListenAddress:    getEnvWithDefault("LISTEN_ADDRESS", ":8080"),
		BankServerHost:   getEnvWithDefault("BANK_SERVER_SERVICE_HOST", "bank-server-api"),
		BankServerPort:   getEnvWithDefault("BANK_SERVER_SERVICE_PORT", "8443"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),

		// Optional, not mustGetEnv: bank-agent's invoke URL only exists once
		// its Terraform has been applied, which itself needs bank-server
		// already running in this cluster — deploy-static.sh deploys
		// bank-client once before that's available. Leaving these unset
		// disables sign-in and chat but still serves the dashboard.
		OIDCDiscoveryURL:   getEnvWithDefault("OIDC_DISCOVERY_URL", ""),
		OIDCClientID:       getEnvWithDefault("OIDC_CLIENT_ID", ""),
		OIDCRedirectURL:    getEnvWithDefault("OIDC_REDIRECT_URL", ""),
		BankAgentInvokeURL: getEnvWithDefault("BANK_AGENT_INVOKE_URL", ""),
		SessionSecret:      getEnvWithDefault("SESSION_SECRET", ""),
	}

	switch authMode {
	case authModeStatic:
		env.StaticClientAPIKey = mustGetEnv("STATIC_CLIENT_API_KEY")
	case authModeSPIFFE:
		env.ServerSPIFFEID = mustGetEnv("SERVER_SPIFFE_ID")
	default:
		slog.Error("Invalid AUTH_MODE", "value", authMode)
		os.Exit(1)
	}

	return env
}

func run(ctx context.Context, env *Env) error {
	fetcher, err := buildFetcher(ctx, env)
	if err != nil {
		return err
	}

	tmpl, err := template.ParseFS(templatesFS, "templates/dashboard.html.tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse dashboard template: %w", err)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	// bank-agent's invoke call is an agentic round trip — cold-starting the
	// AgentCore Runtime, a Bedrock Converse call, a tool call out to
	// bank-server, and a second Bedrock call to compose the answer — so it
	// needs a much longer budget than the OIDC calls above.
	agentClient := &http.Client{Timeout: 60 * time.Second}

	sessionSecret := env.SessionSecret
	if sessionSecret == "" {
		slog.Warn("SESSION_SECRET not set — generating an ephemeral one; sessions won't survive a restart")
		generated, err := randomToken(24)
		if err != nil {
			return fmt.Errorf("failed to generate a session secret: %w", err)
		}
		sessionSecret = generated
	}
	sessions := &sessionStore{secret: []byte(sessionSecret)}

	chatEnabled := env.OIDCDiscoveryURL != "" && env.BankAgentInvokeURL != ""

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard(fetcher, tmpl, env.AuthMode, sessions, chatEnabled))

	if chatEnabled {
		slog.Info("Discovering OIDC endpoints", "issuer", env.OIDCDiscoveryURL)
		oidcDoc, err := discoverOIDC(env.OIDCDiscoveryURL, httpClient)
		if err != nil {
			return fmt.Errorf("failed to discover OIDC endpoints: %w", err)
		}
		mux.HandleFunc("/login", handleLogin(oidcDoc, env.OIDCClientID, env.OIDCRedirectURL))
		mux.HandleFunc("/callback", handleCallback(oidcDoc, httpClient, env.OIDCClientID, env.OIDCRedirectURL, sessions))
		mux.HandleFunc("/logout", handleLogout(sessions))
		mux.HandleFunc("/api/chat", handleChat(sessions, agentClient, env.BankAgentInvokeURL))
	} else {
		slog.Info("OIDC_DISCOVERY_URL/BANK_AGENT_INVOKE_URL not set — sign-in and chat are disabled")
	}

	server := &http.Server{
		Addr:              env.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("bank-client starting", "address", env.ListenAddress, "auth_mode", env.AuthMode)
	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}

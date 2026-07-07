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

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard(fetcher, tmpl, env.AuthMode))

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

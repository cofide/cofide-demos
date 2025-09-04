package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	cofide_http_server "github.com/cofide/cofide-sdk-go/http/server"
	"github.com/cofide/cofide-sdk-go/pkg/id"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Fatal error, exiting", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	SecurePort   string
	InsecurePort string
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnv() *Env {
	return &Env{
		SecurePort:   getEnvWithDefault("SECURE_PORT", ":8443"),
		InsecurePort: getEnvWithDefault("INSECURE_PORT", ":8080"),
	}
}

func run(ctx context.Context, env *Env) error {
	serverCtx := ctx

	secureMux := http.NewServeMux()
	secureServer := cofide_http_server.NewServer(&http.Server{
		Addr:    env.SecurePort,
		Handler: secureMux,
	}, cofide_http_server.WithSVIDMatch(id.Equals("ns", "production")),
	)
	secureMux.HandleFunc("/", handler(secureServer))

	insecureMux := http.NewServeMux()
	insecureServer := &http.Server{
		Addr:    env.InsecurePort,
		Handler: insecureMux,
	}
	insecureMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("...pong from insecure server"))
		if err != nil {
			slog.Error("Insecure server error", "error", err)
		}
	})

	go func() {
		fmt.Printf("Starting secure server on %s\n", env.SecurePort)
		if err := secureServer.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("Secure server error", "error", err)
		}
	}()

	go func() {
		fmt.Printf("Starting insecure server on %s\n", env.InsecurePort)
		if err := insecureServer.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("Insecure server error", "error", err)
		}
	}()

	<-serverCtx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := secureServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("secure server shutdown failed: %w", err)
	}

	if err := insecureServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("insecure server shutdown failed: %w", err)
	}

	return nil
}

func handler(server *cofide_http_server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			slog.Error("No client certificate provided")
			http.Error(w, "Error: No client certificate provided", http.StatusUnauthorized)
			return
		}

		// Extract the SPIFFE ID from the peer certificate.
		peerCert := r.TLS.PeerCertificates[0]
		clientID, err := x509svid.IDFromCert(peerCert)
		if err != nil {
			slog.Error("Error getting SPIFFE ID from peer cert", "error", err)
			http.Error(w, "Error: Invalid client SVID", http.StatusUnauthorized)
			return
		}
		slog.Info("ping", slog.String("client.id", clientID.String()))

		identity, err := server.GetIdentity()
		if err != nil {
			slog.Error("Error getting server identity", "error", err)
			http.Error(w, "Failed to get server identity", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(fmt.Sprintf("...pong from %s", identity.ToSpiffeID().String())))
		if err != nil {
			slog.Error("Error writing response", "error", err)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	cofide_http_server "github.com/cofide/cofide-sdk-go/http/server"
	"github.com/cofide/cofide-sdk-go/pkg/id"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal("", err)
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
	}, cofide_http_server.WithSVIDMatch(id.Equals("sa", "ping-pong-client")),
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
			fmt.Printf("insecure server error: %v\n", err)
		}
	})

	go func() {
		fmt.Printf("Starting secure server on %s\n", env.SecurePort)
		if err := secureServer.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("secure server error: %v\n", err)
		}
	}()

	go func() {
		fmt.Printf("Starting insecure server on %s\n", env.InsecurePort)
		if err := insecureServer.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("insecure server error: %v\n", err)
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
		identity, err := server.GetIdentity()
		if err != nil {
			http.Error(w, "Failed to get server identity", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(fmt.Sprintf("...pong from %s", identity.ToSpiffeID().String())))
		if err != nil {
			fmt.Printf("server error: %v\n", err)
		}
	}
}

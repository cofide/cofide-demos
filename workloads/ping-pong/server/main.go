package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal("", err)
	}
}

type Env struct {
	Port      string
	TLSCert   string
	TLSKey    string
	EnableTLS bool
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnv() *Env {
	certPath := getEnvWithDefault("TLS_CERT_PATH", "/etc/certs/tls.crt")
	keyPath := getEnvWithDefault("TLS_KEY_PATH", "/etc/certs/tls.key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	enableTLS := certErr == nil && keyErr == nil

	if enableTLS {
		log.Printf("TLS enabled with cert: %s, key: %s", certPath, keyPath)
	} else {
		log.Printf("TLS disabled: cert or key not found at %s, %s", certPath, keyPath)
	}

	return &Env{
		Port:      getEnvWithDefault("PORT", ":9090"),
		TLSCert:   certPath,
		TLSKey:    keyPath,
		EnableTLS: enableTLS,
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	server := &http.Server{
		Addr:              env.Port,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	if env.EnableTLS {
		log.Printf("Starting TLS server on port %s", env.Port)
		if err := server.ListenAndServeTLS(env.TLSCert, env.TLSKey); err != nil {
			return fmt.Errorf("failed to serve TLS: %w", err)
		}
	} else {
		log.Printf("Starting non-TLS server on port %s", env.Port)
		if err := server.ListenAndServe(); err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}
	}

	return nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("...pong"))
}

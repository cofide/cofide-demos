package main

import (
	"context"
	"expvar"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Metrics counters
var (
	successfulConnections = expvar.NewInt("successful_connections")
	handlerErrors         = expvar.NewInt("handler_errors")
	svidFailures          = expvar.NewInt("svid_failures")
	tlsErrors             = expvar.NewInt("tls_errors")
	requestsTotal         = expvar.NewInt("requests_total")
	serverStartTime       = expvar.NewInt("server_start_time")
)

func main() {
	serverStartTime.Set(time.Now().Unix())

	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal(err)
	}
}

type Env struct {
	Port             string
	SpiffeSocketPath string
	MetricsEnabled   bool
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
		Port:             getEnvWithDefault("PORT", ":8443"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", metricsWrapper(handler))
	// Expose metrics endpoint
	mux.Handle("/debug/vars", expvar.Handler())

	source, err := workloadapi.NewX509Source(ctx,
		workloadapi.WithClientOptions(
			workloadapi.WithAddr(env.SpiffeSocketPath),
		),
	)
	if err != nil {
		svidFailures.Add(1)
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer source.Close()

	tlsConfig := tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeAny())
	server := &http.Server{
		Addr:              env.Port,
		TLSConfig:         tlsConfig,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	log.Printf("Server starting on %s with metrics at /debug/vars", env.Port)

	if err := server.ListenAndServeTLS("", ""); err != nil {
		tlsErrors.Add(1)
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func metricsWrapper(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestsTotal.Add(1)
		successfulConnections.Add(1)
		next(w, r)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("...pong"))
	if err != nil {
		handlerErrors.Add(1)
		log.Printf("Error writing response: %v", err)
		return
	}
}

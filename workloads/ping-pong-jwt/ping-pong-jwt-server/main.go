package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	workloadAPITimeout = 30 * time.Second
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal(err)
	}
}

type Env struct {
	Address          string
	SpiffeSocketPath string
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
		Address:          getEnvWithDefault("PING_PONG_SERVER_LISTEN_ADDRESS", ":8443"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
	}
}

type pingPongServer struct {
	wlClient *workloadapi.Client
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, workloadAPITimeout)
	defer cancel()

	slog.Info("Creating workload client")
	client, err := workloadapi.New(ctx, workloadapi.WithAddr(env.SpiffeSocketPath))
	if err != nil {
		return fmt.Errorf("failed to create workload client: %w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	slog.Info("Created workload client")

	pps := &pingPongServer{wlClient: client}
	mux := http.NewServeMux()
	mux.HandleFunc("/", pps.handler)

	server := &http.Server{
		Addr:              env.Address,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	slog.Info("Server starting")
	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func (s *pingPongServer) handler(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("No token provided"))
		return
	}

	token := auth[7:]
	audience := "ping-pong-server"
	if _, err := s.wlClient.ValidateJWTSVID(r.Context(), token, audience); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid token provided"))
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("...pong"))
	if err != nil {
		slog.Error("Error writing response", "error", err)
		return
	}
}

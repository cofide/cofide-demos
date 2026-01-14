package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
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
	ClientSPIFFEID   string
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
	return &Env{
		Address:          getEnvWithDefault("PING_PONG_SERVER_LISTEN_ADDRESS", ":8443"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
		ClientSPIFFEID:   mustGetEnv("CLIENT_SPIFFE_ID"),
	}
}

type pingPongServer struct {
	wlClient         *workloadapi.Client
	authorizedClient spiffeid.ID
}

func run(ctx context.Context, env *Env) error {
	initCtx, cancel := context.WithTimeout(ctx, workloadAPITimeout)
	defer cancel()

	slog.Info("Fetching server JWT-SVID")
	source, err := workloadapi.NewJWTSource(initCtx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
	if err != nil {
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer func() { _ = source.Close() }()

	slog.Info("Creating workload client")
	client, err := workloadapi.New(ctx, workloadapi.WithAddr(env.SpiffeSocketPath))
	if err != nil {
		return fmt.Errorf("failed to create workload client: %w", err)
	}
	defer func() { _ = client.Close() }()

	pps := &pingPongServer{
		wlClient:         client,
		authorizedClient: spiffeid.RequireFromString(env.ClientSPIFFEID),
	}
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
		_, _ = w.Write([]byte("No token provided by client"))
		return
	}

	token := auth[7:]
	audience := "ping-pong-server"
	clientSVID, err := s.wlClient.ValidateJWTSVID(r.Context(), token, audience)
	if err != nil {
		slog.Error("Invalid client token", "error", err.Error())
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid client token provided"))
		return
	}

	clientId := clientSVID.ID
	slog.Info("Received ping from client", "id", clientId)
	matcher := spiffeid.MatchID(s.authorizedClient)
	if err := matcher(clientId); err != nil {
		slog.Info("Rejected unauthorized request", "id", clientId)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid client ID"))
		return
	}

	// Send server SVID to client for mutual verification
	svid, err := s.wlClient.FetchJWTSVID(r.Context(), jwtsvid.Params{Audience: "ping-pong-client"})
	if err != nil {
		slog.Error("Failed to fetch server JWT-SVID", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal server error"))
		return
	}
	w.Header().Set("Authorization", fmt.Sprintf("Bearer %s", svid.Marshal()))


	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte("...pong"))
	if err != nil {
		slog.Error("Error writing response", "error", err)
		return
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal(err)
	}
}

type Env struct {
	ServerURL        string
	SpiffeSocketPath string
	ServerSPIFFEID   string
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
		ServerURL:        getEnvWithDefault("PING_PONG_SERVICE_URL", "https://ping-pong-server.demo"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
		ServerSPIFFEID:   mustGetEnv("SERVER_SPIFFE_ID"),
	}
}

func run(ctx context.Context, env *Env) error {
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	source, err := workloadapi.NewJWTSource(initCtx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
	if err != nil {
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()

	slog.Info("Creating workload client")
	wlClient, err := workloadapi.New(initCtx, workloadapi.WithAddr(env.SpiffeSocketPath))
	if err != nil {
		return fmt.Errorf("failed to create workload client: %w", err)
	}
	defer func() { _ = wlClient.Close() }()
	c := pingPongClient{wlClient: wlClient}

	client := &http.Client{}
	var svid *jwtsvid.SVID
	for {
		if svid == nil || time.Until(svid.Expiry) < time.Minute {
			slog.Info("Fetching JWT-SVID")
			svid, err = source.FetchJWTSVID(ctx, jwtsvid.Params{Audience: "ping-pong-server"})
			if err != nil {
				return fmt.Errorf("failed to obtain JWT-SVID: %w", err)
			}
			slog.Info("Fetched JWT-SVID")
		}

		slog.Info("ping...")
		if err := c.ping(client, env, svid.Marshal()); err != nil {
			slog.Error("problem reaching server", "error", err)
		}
		time.Sleep(5 * time.Second)
	}
}

type pingPongClient struct {
	wlClient *workloadapi.Client
}

func (c *pingPongClient) ping(client *http.Client, env *Env, clientToken string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, env.ServerURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", clientToken))

	r, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = r.Body.Close()
	}()

	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " {
		return errors.New("no token provided by server")
	}

	// Parse server SVID from bearer token header
	serverToken := auth[7:]
	audience := "ping-pong-client"
	serverSVID, err := c.wlClient.ValidateJWTSVID(context.Background(), serverToken, audience)
	if err != nil {
		return fmt.Errorf("invalid server token: %w", err)
	}

	// Verify server SVID is authorised
	expectedServerID := env.ServerSPIFFEID
	matcher := spiffeid.MatchID(spiffeid.RequireFromString(expectedServerID))
	if err := matcher(serverSVID.ID); err != nil {
		return fmt.Errorf("invalid server ID: %w", err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d: %s", r.StatusCode, body)
	}
	slog.Info(string(body), "from", serverSVID.ID)
	return nil
}

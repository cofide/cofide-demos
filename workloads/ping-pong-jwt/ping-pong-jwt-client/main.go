package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

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
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	source, err := workloadapi.NewJWTSource(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
	if err != nil {
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()

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
		if err := ping(client, env.ServerURL, svid.Marshal()); err != nil {
			slog.Error("problem reaching server", "error", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func ping(client *http.Client, serverURL string, token string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, serverURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	r, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = r.Body.Close()
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d: %s", r.StatusCode, body)
	}
	slog.Info(string(body))
	return nil
}

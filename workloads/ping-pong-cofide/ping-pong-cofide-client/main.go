package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	cofidehttp "github.com/cofide/cofide-sdk-go/http/client"
)

func main() {
	setupLogging()
	env, err := newEnv()
	if err != nil {
		slog.Error("Failed to process environment variables", "error", err)
		os.Exit(1)
	}
	if err := run(context.Background(), env); err != nil {
		slog.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func setupLogging() {
	logOpts := &slog.HandlerOptions{Level: slog.LevelDebug}
	logger := slog.New(slog.NewTextHandler(os.Stderr, logOpts))
	slog.SetDefault(logger)
}

type env struct {
	serverAddress string
	serverPort    int
	xdsServerURI  string
	xdsNodeID     string
}

func getEnv(variable string) (string, error) {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return "", fmt.Errorf("missing required environment variable %s", variable)
	}
	return v, nil
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnvIntWithDefault(variable string, defaultValue int) int {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}

	intValue, err := strconv.Atoi(v)
	if err != nil {
		return defaultValue
	}

	return intValue
}

func newEnv() (*env, error) {
	xdsServerURI, err := getEnv("XDS_SERVER_URI")
	if err != nil {
		return nil, err
	}
	return &env{
		serverAddress: getEnvWithDefault("PING_PONG_SERVICE_HOST", "ping-pong-server.demo"),
		serverPort:    getEnvIntWithDefault("PING_PONG_SERVICE_PORT", 8443),
		xdsServerURI:  xdsServerURI,
		xdsNodeID:     getEnvWithDefault("XDS_NODE_ID", "node"),
	}, nil
}

func run(ctx context.Context, env *env) error {
	client, err := cofidehttp.NewClient(
		cofidehttp.WithXDS(env.xdsServerURI),
		cofidehttp.WithXDSNodeID(env.xdsNodeID),
	)
	if err != nil {
		return fmt.Errorf("failed creating Cofide HTTP client: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			identity, err := client.GetIdentity()
			if err != nil {
				slog.Error("problem obtaining client identity", "error", err)
			}
			slog.Info(fmt.Sprintf("ping from %s...", identity.ToSpiffeID().String()))
			if err := ping(client, env.serverAddress, env.serverPort); err != nil {
				slog.Error("problem reaching server", "error", err)
			}
			time.Sleep(5 * time.Second)
		}
	}
}

func ping(client *cofidehttp.Client, serverAddr string, serverPort int) error {
	url := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", serverAddr, serverPort),
	}

	r, err := client.Get(url.String())
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
		// Limit how much of the response we include in the error
		body = body[:min(len(body), 1024)]
		return fmt.Errorf("unexpected status code: %d: %s", r.StatusCode, body)
	}
	slog.Info(string(body))
	return nil
}

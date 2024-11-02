package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"time"

	cofide_http "github.com/cofide/cofide-sdk-go/http/client"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal("", err)
	}
}

type Env struct {
	ServerAddress string
	ServerPort    int
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

func getEnv() *Env {
	return &Env{
		ServerAddress: getEnvWithDefault("PING_PONG_SERVICE_HOST", "ping-pong-server.demo"),
		ServerPort:    getEnvIntWithDefault("PING_PONG_SERVICE_PORT", 8443),
	}
}

func run(ctx context.Context, env *Env) error {
	client := cofide_http.NewClient()

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
			if err := ping(client, env.ServerAddress, env.ServerPort); err != nil {
				slog.Error("problem reaching server", "error", err)
			}
			time.Sleep(5 * time.Second)
		}
	}
}

func ping(client *cofide_http.Client, serverAddr string, serverPort int) error {
	url := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", serverAddr, serverPort),
	}

	r, err := client.Get(url.String())
	if err != nil {
		return err
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	slog.Info(string(body))
	return nil
}

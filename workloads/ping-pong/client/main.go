package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	for {
		slog.Info("ping...")
		if err := ping(client, env.ServerAddress, env.ServerPort); err != nil {
			slog.Error("problem reaching server", "error", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func ping(client *http.Client, serverAddr string, serverPort int) error {
	r, err := client.Get((&url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s:%d", serverAddr, serverPort),
	}).String())

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

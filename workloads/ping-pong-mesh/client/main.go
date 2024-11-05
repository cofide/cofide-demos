package main

import (
	"context"
	"io"
	"log"
	"log/slog"
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
	ServerAddress string
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
		ServerAddress: getEnvWithDefault("SERVER_ADDRESS", "http://cofide.mesh.global"),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{},
	}

	for {
		slog.Info("ping...")
		if err := ping(client, env.ServerAddress); err != nil {
			slog.Error("problem reaching server", "error", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func ping(client *http.Client, serverAddr string) error {
	r, err := client.Get(serverAddr)
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

package main

import (
	"context"
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
		log.Fatal(err)
	}
}

type Env struct {
	ServerAddress string
}

func getEnvOrPanic(variable string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		panic(fmt.Sprintf("expected environment variable %s not set", variable))
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
		ServerAddress: getEnvOrPanic("PING_PONG_SERVICE_HOST"),
	}
}

func run(ctx context.Context, env *Env) error {
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
	r, err := client.Get((&url.URL{
		Scheme: "http",
		Host:   serverAddr,
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

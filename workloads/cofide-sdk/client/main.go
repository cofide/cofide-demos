package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
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
		ServerAddress: getEnvWithDefault("SERVER_ADDRESS", "http://server.cofide"),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := cofide_http.NewClient(
	/*
		cofide_http.WithCustomResolver(
			&net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
					d := net.Dialer{
						Timeout: time.Millisecond * time.Duration(500),
					}
					return d.DialContext(ctx, network, "cofide-agent.cofide.svc.cluster.local:8080")
				},
			},
		),
	*/
	)

	for {
		slog.Info("ping...")
		if err := ping(client, env.ServerAddress); err != nil {
			slog.Error("problem reaching server", "error", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func ping(client *cofide_http.Client, serverAddr string) error {
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

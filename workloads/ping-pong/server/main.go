package main

import (
	"context"
	"fmt"
	"log"
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
	Port string
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
		Port: getEnvWithDefault("PORT", ":9090"),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	server := &http.Server{
		Addr:              env.Port,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("...pong"))
}
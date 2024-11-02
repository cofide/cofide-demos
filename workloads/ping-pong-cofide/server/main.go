package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	cofide_http_server "github.com/cofide/cofide-sdk-go/http/server"
	"github.com/cofide/cofide-sdk-go/id"
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
		Port: getEnvWithDefault("PORT", ":8443"),
	}
}

func run(ctx context.Context, env *Env) error {
	serverCtx := ctx

	mux := http.NewServeMux()

	server := cofide_http_server.NewServer(&http.Server{
		Addr:    env.Port,
		Handler: mux,
	}, cofide_http_server.WithSVIDMatch(id.Equals("sa", "ping-pong-client")),
	)

	mux.HandleFunc("/", handler(server))

	go func() {
		fmt.Println("Starting secure server on :8443")
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("server error: %v\n", err)
		}
	}()

	<-serverCtx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	return nil
}

func handler(server *cofide_http_server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := server.GetIdentity()
		if err != nil {
			http.Error(w, "Failed to get server identity", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("...pong from %s", identity.ToSpiffeID().String())))
	}
}

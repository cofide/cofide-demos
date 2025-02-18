package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	socketPath    = "unix:///spiffe-workload-api/spire-agent.sock"
	serverAddress = "https://consumer.production.svc.cluster.local:9090"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)))
	if err != nil {
		return fmt.Errorf("unable to create X509Source: %w", err)
	}
	defer source.Close()

	spiffeID := fmt.Sprintf(
		"spiffe://%s/ns/production/sa/default",
		os.Getenv("TRUST_DOMAIN"),
	)
	allowedSPIFFEID := spiffeid.RequireFromString(spiffeID)
	tlsConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeID(allowedSPIFFEID))
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	for {
		err := getBuckets(*client)
		if err != nil {
			return err
		}
		time.Sleep(5 * time.Second)
	}
}

func getBuckets(client http.Client) error {
	resp, err := client.Get(serverAddress + "/buckets")
	if err != nil {
		return fmt.Errorf("error connecting to %q: %w", serverAddress, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unable to read body: %w", err)
	}

	log.Printf("%s", body)
	return nil
}

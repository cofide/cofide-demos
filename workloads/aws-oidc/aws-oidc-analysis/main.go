package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const socketPath = "unix:///spiffe-workload-api/spire-agent.sock"

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var tlsConfig *tls.Config
	enableTLS := strings.ToLower(os.Getenv("ENABLE_TLS")) == "true"
	if enableTLS {
		source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)))
		if err != nil {
			return fmt.Errorf("unable to create X509Source: %w", err)
		}
		defer source.Close()

		spiffeID := fmt.Sprintf(
			"spiffe://%s/ns/production/sa/default",
			os.Getenv("CONSUMER_TRUST_DOMAIN"),
		)
		allowedSPIFFEID := spiffeid.RequireFromString(spiffeID)
		tlsConfig = tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeID(allowedSPIFFEID))
	}

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	serverAddress := os.Getenv("CONSUMER_SERVER_ADDRESS")
	for {
		err := getBuckets(client, serverAddress)
		if err != nil {
			return err
		}
		time.Sleep(5 * time.Second)
	}
}

func getBuckets(client http.Client, serverAddress string) error {
	resp, err := client.Get(serverAddress + "/buckets")
	if err != nil {
		return fmt.Errorf("error connecting to %q: %w", serverAddress, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unable to read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error from server: %s", body)
	}

	log.Printf("Buckets Found: %s", body)
	return nil
}

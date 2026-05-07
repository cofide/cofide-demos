package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spiffe/go-spiffe/v2/logger"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/api/iterator"
)

const (
	audience   = "consumer-workload"
	socketPath = "unix:///spiffe-workload-api/spire-agent.sock"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal("", err)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	router := gin.Default()
	router.GET("/", getRoot)
	router.GET("/buckets", getBuckets)

	var tlsConfig *tls.Config
	enableTLS := strings.ToLower(os.Getenv("ENABLE_TLS")) == "true"
	if enableTLS {
		slog.Info("Waiting for X.509 SVID")
		source, err := workloadapi.NewX509Source(
			ctx,
			workloadapi.WithClientOptions(
				workloadapi.WithAddr(socketPath),
				workloadapi.WithLogger(logger.Std),
			),
		)
		if err != nil {
			return fmt.Errorf("unable to create X509Source: %w", err)
		}
		defer func() {
			_ = source.Close()
		}()
		slog.Info("Retrieved X.509 SVID")

		var analysisSPIFFEID string
		analysisSPIFFEID, ok := os.LookupEnv("ANALYSIS_SPIFFE_ID")
		if !ok {
			// Default expected SPIFFE ID for analysis workload
			analysisSPIFFEID = "spiffe://%s/ns/analytics/sa/default"
		}

		spiffeID := fmt.Sprintf(
			analysisSPIFFEID,
			os.Getenv("ANALYSIS_TRUST_DOMAIN"),
		)
		allowedSPIFFEID := spiffeid.RequireFromString(spiffeID)
		tlsConfig = tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeID(allowedSPIFFEID))
	}

	server := &http.Server{
		Addr:              ":9090",
		Handler:           router,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: time.Second * 10,
	}

	if enableTLS {
		if err := server.ListenAndServeTLS("", ""); err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}
	} else {
		if err := server.ListenAndServe(); err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}
	}
	return nil
}

func throw500(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func getRoot(c *gin.Context) {
	c.String(http.StatusOK, "Success")
}

func getBuckets(c *gin.Context) {
	workloadAPI, err := workloadapi.New(context.Background())
	if err != nil {
		throw500(c, err)
		return
	}

	workloadIdentityProvider := os.Getenv("GCP_WORKLOAD_IDENTITY_PROVIDER")
	if workloadIdentityProvider == "" {
		throw500(c, fmt.Errorf("GCP_WORKLOAD_IDENTITY_PROVIDER environment variable not set"))
		return
	}

	gcpProjectID := os.Getenv("GCP_PROJECT_ID")
	if gcpProjectID == "" {
		throw500(c, fmt.Errorf("GCP_PROJECT_ID environment variable not set"))
		return
	}

	gcsClient, err := initGCSClient(c.Request.Context(), workloadIdentityProvider, workloadAPI)
	if err != nil {
		throw500(c, err)
		return
	}

	var buckets []string
	bucketsIt := gcsClient.Buckets(c.Request.Context(), gcpProjectID)
	for {
		bucketAttrs, err := bucketsIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			throw500(c, err)
			return
		}
		buckets = append(buckets, bucketAttrs.Name)
	}

	c.IndentedJSON(http.StatusOK, buckets)
}

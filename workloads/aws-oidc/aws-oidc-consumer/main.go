package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spiffe/go-spiffe/v2/logger"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	audience    = "consumer-workload"
	sessionName = "consumer-workload-session"
	socketPath  = "unix:///spiffe-workload-api/spire-agent.sock"
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
		defer source.Close()

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

	retriever := NewJWTSVIDRetriever(workloadAPI, audience)

	cfg, err := loadAWSConfig(retriever)
	if err != nil {
		throw500(c, err)
		return
	}

	buckets, err := getS3Buckets(*cfg)
	if err != nil {
		throw500(c, err)
		return
	}

	c.IndentedJSON(http.StatusOK, buckets)
}

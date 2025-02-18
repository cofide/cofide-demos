package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/gin-gonic/gin"
	"github.com/spiffe/go-spiffe/v2/logger"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
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

	// Create a `workloadapi.X509Source`, it will connect to Workload API using provided socket.
	source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath), workloadapi.WithLogger(logger.Std)))
	if err != nil {
		return fmt.Errorf("unable to create X509Source: %w", err)
	}
	defer source.Close()

	spiffeID := fmt.Sprintf(
		"spiffe://%s/ns/analytics/sa/default",
		os.Getenv("TRUST_DOMAIN"),
	)
	allowedSPIFFEID := spiffeid.RequireFromString(spiffeID)
	tlsConfig := tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeID(allowedSPIFFEID))
	server := &http.Server{
		Addr:              ":9090",
		Handler:           router,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: time.Second * 10,
	}

	if err := server.ListenAndServeTLS("", ""); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}

// getBuckets responds with a list of all the S3 buckets as JSON.
func getBuckets(c *gin.Context) {
	jwt, err := getJWT()
	if err != nil {
		fmt.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	err = writeJWTToFile(*jwt, tokenFilePath)
	if err != nil {
		fmt.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cfg, err := loadAWSConfig()
	if err != nil {
		fmt.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	buckets, err := getS3Buckets(*cfg)
	if err != nil {
		fmt.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.IndentedJSON(http.StatusOK, buckets)
}

func getRoot(c *gin.Context) {
	c.String(http.StatusOK, "Success")
}

func getS3Buckets(cfg aws.Config) ([]Bucket, error) {
	s3Client := s3.NewFromConfig(cfg)
	resp, err := s3Client.ListBuckets(context.TODO(), &s3.ListBucketsInput{})
	if err != nil {
		log.Fatalf("failed to list the buckets, %v", err)
	}

	buckets := make([]Bucket, 0)
	for _, bucket := range resp.Buckets {
		buckets = append(buckets, Bucket{
			Name:         *bucket.Name,
			CreationDate: *bucket.CreationDate,
		})
	}

	return buckets, nil
}

func loadAWSConfig() (*aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("eu-west-1"))
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config, %v", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	roleArn := os.Getenv("AWS_ROLE_ARN")
	log.Printf("Role ARN: %s", roleArn)
	appCreds := aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(
		stsClient,
		roleArn,
		stscreds.IdentityTokenFile(tokenFilePath),
		func(o *stscreds.WebIdentityRoleOptions) { o.RoleSessionName = sessionName },
	))

	cfg.Credentials = appCreds

	return &cfg, nil
}

func writeJWTToFile(jwt string, filepath string) error {
	err := os.WriteFile(filepath, []byte(jwt), 0644)
	if err != nil {
		return fmt.Errorf("failed to write the JWT to the file, %v", err)
	}
	return nil
}

func getJWT() (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientOptions := workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath))
	jwtSource, err := workloadapi.NewJWTSource(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to create JWTSource: %w", err)
	}
	defer jwtSource.Close()

	svid, err := jwtSource.FetchJWTSVID(ctx, jwtsvid.Params{
		Audience: audience,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to fetch SVID: %w", err)
	}

	jwt := svid.Marshal()
	log.Printf("JWT: %s", jwt)
	return &jwt, nil
}

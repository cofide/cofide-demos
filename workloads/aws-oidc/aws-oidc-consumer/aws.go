package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

func loadAWSConfig(retriever *JWTSVIDRetriever) (*aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("eu-west-1"))
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config, %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	roleARN, ok := os.LookupEnv("AWS_ROLE_ARN")
	if !ok {
		return nil, fmt.Errorf("AWS_ROLE_ARN environment variable not set")
	}

	creds := stscreds.NewWebIdentityRoleProvider(
		stsClient,
		roleARN,
		retriever, (func(opts *stscreds.WebIdentityRoleOptions) {
			opts.RoleSessionName = sessionName
			opts.Duration = 15 * time.Minute
		}))

	cfg.Credentials = aws.NewCredentialsCache(creds)

	return &cfg, nil
}

type Bucket struct {
	Name         string
	CreationDate time.Time
}

func getS3Buckets(cfg aws.Config) ([]Bucket, error) {
	s3Client := s3.NewFromConfig(cfg)
	resp, err := s3Client.ListBuckets(context.TODO(), &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("unable to list S3 buckets, %w", err)
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

// jwtSvidCredRetreiver is an implementation of an IdentityTokenRetriever for SPIFFE JWT-SVID tokens
// https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials/stscreds#IdentityTokenRetriever

// TODO: roll this into cofide-go-sdk helpers
type JWTSVIDRetriever struct {
	workloadAPI *workloadapi.Client
	audience    string
}

func NewJWTSVIDRetriever(workloadAPI *workloadapi.Client, audience string) *JWTSVIDRetriever {
	return &JWTSVIDRetriever{
		workloadAPI: workloadAPI,
		audience:    audience,
	}
}

func (r JWTSVIDRetriever) GetIdentityToken() ([]byte, error) {
	token, err := r.fetchSPIFFEToken()
	if err != nil {
		return nil, fmt.Errorf("error fetching SPIFFE token: %w", err)
	}

	return []byte(token), nil
}

func (r JWTSVIDRetriever) fetchSPIFFEToken() (string, error) {
	jwt, err := r.workloadAPI.FetchJWTSVID(context.TODO(), jwtsvid.Params{
		Audience: r.audience,
	})

	if err != nil {
		return "", err
	}

	slog.Info("fetched JWT-SVID", "component", "jwtsvid_cred_retriever")

	return jwt.Marshal(), nil
}

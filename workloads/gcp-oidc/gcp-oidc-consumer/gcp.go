package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	gcsstorage "cloud.google.com/go/storage"
	"golang.org/x/oauth2/google/externalaccount"
	"google.golang.org/api/option"
)

// JWTSVIDSubTknSupplier is an implementation of an IdentityTokenRetriever for SPIFFE JWT-SVID tokens
type JWTSVIDSubTknSupplier struct {
	workloadAPI *workloadapi.Client
	Audience    string
}

var _ externalaccount.SubjectTokenSupplier = &JWTSVIDSubTknSupplier{}

func NewJWTSVIDSubTknSupplier(workloadAPI *workloadapi.Client, audience string) (*JWTSVIDSubTknSupplier, error) {
	return &JWTSVIDSubTknSupplier{
		workloadAPI: workloadAPI,
		Audience:    audience,
	}, nil
}

// SubjectToken fulfils https://pkg.go.dev/golang.org/x/oauth2/google/externalaccount#SubjectTokenSupplier
func (r JWTSVIDSubTknSupplier) SubjectToken(ctx context.Context, _ externalaccount.SupplierOptions) (string, error) {
	return r.fetchSPIFFEToken(ctx)
}

func (r JWTSVIDSubTknSupplier) fetchSPIFFEToken(ctx context.Context) (string, error) {
	jwt, err := r.workloadAPI.FetchJWTSVID(ctx, jwtsvid.Params{
		Audience: r.Audience,
	})
	if err != nil {
		return "", err
	}

	slog.Info("Fetched JWT-SVID", "component", "jwtsvid_cred_retriever", "id", jwt.ID)

	return jwt.Marshal(), nil
}

// LoadGCPConfig uses a token supplier to get temporary credentials through a workload identity provider.
func LoadGCPConfig(workloadIdentityProvider string, subTknSupplier externalaccount.SubjectTokenSupplier, scopes []string) externalaccount.Config {
	return externalaccount.Config{
		Audience:             "//iam.googleapis.com/" + workloadIdentityProvider,
		TokenURL:             "https://sts.googleapis.com/v1/token",
		SubjectTokenType:     "urn:ietf:params:oauth:token-type:jwt",
		SubjectTokenSupplier: subTknSupplier,
		Scopes:               scopes,
	}
}

func initGCPConfig(audience, workloadIdentityProvider string, scopes []string, workloadAPI *workloadapi.Client) (externalaccount.Config, error) {
	subTknSupplier, err := NewJWTSVIDSubTknSupplier(workloadAPI, audience)
	if err != nil {
		return externalaccount.Config{}, err
	}

	return LoadGCPConfig(workloadIdentityProvider, subTknSupplier, scopes), nil
}

type GCPOIDCConfig struct {
	WorkloadIdentityProvider string
	Audience                 string
}

func initGCSClient(ctx context.Context, workloadIdentityProvider string, workloadAPI *workloadapi.Client) (*gcsstorage.Client, error) {
	var clientOpts []option.ClientOption

	// OIDC, setup SVID exchange
	gcpCfg, err := initGCPConfig(audience, workloadIdentityProvider, []string{"https://www.googleapis.com/auth/devstorage.read_write"}, workloadAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise GCP config: %w", err)
	}
	ts, err := externalaccount.NewTokenSource(
		ctx,
		gcpCfg,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create token source: %w", err)
	}
	clientOpts = append(clientOpts, option.WithTokenSource(ts))

	gcsClient, err := gcsstorage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}
	return gcsClient, nil
}

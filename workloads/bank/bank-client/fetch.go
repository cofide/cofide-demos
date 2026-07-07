package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// summaryFetcher calls bank-server's /api/summary endpoint. In static mode
// this is a plain HTTP GET with a bearer API key; in spiffe mode it's an
// mTLS GET authenticated with the client's X.509-SVID.
type summaryFetcher struct {
	client *http.Client
	url    string
	apiKey string
}

func buildFetcher(ctx context.Context, env *Env) (*summaryFetcher, error) {
	url := fmt.Sprintf("%s://%s:%s/api/summary", schemeFor(env.AuthMode), env.BankServerHost, env.BankServerPort)

	switch env.AuthMode {
	case authModeStatic:
		return &summaryFetcher{client: &http.Client{}, url: url, apiKey: env.StaticClientAPIKey}, nil
	case authModeSPIFFE:
		source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
		if err != nil {
			return nil, fmt.Errorf("unable to obtain X.509 SVID: %w", err)
		}
		serverSPIFFEID, err := spiffeid.FromString(env.ServerSPIFFEID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse SERVER_SPIFFE_ID: %w", err)
		}
		tlsConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeOneOf(serverSPIFFEID))
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
		return &summaryFetcher{client: client, url: url}, nil
	default:
		return nil, fmt.Errorf("invalid AUTH_MODE: %s", env.AuthMode)
	}
}

func schemeFor(authMode string) string {
	if authMode == authModeSPIFFE {
		return "https"
	}
	return "http"
}

func (f *summaryFetcher) fetch(ctx context.Context) (Summary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Summary{}, err
	}
	if f.apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.apiKey))
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return Summary{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Summary{}, fmt.Errorf("unexpected status from bank-server: %d", resp.StatusCode)
	}

	var summary Summary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return Summary{}, fmt.Errorf("failed to decode summary: %w", err)
	}
	return summary, nil
}

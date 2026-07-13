// Command bank-fraud-checker is a small poller that calls bank-server's
// fraud-check API every POLL_INTERVAL: it lists transactions, logs how many
// haven't been reviewed yet, and if any are outstanding, marks them checked.
//
// It's the bank demo's 5th workload, and the odd one out architecturally: it
// doesn't run in Kubernetes (bank-client/bank-server) or on AWS-managed
// compute with no Workload API access (bank-lambda/bank-agent, which need
// Cofide Credex to bridge an AWS-native credential into a SPIFFE credential).
// It runs as a plain Docker container on a VM alongside a real, co-located
// SPIRE agent — visible to Cofide Connect via cofide-node-observer, the same
// way cofide-observer makes Kubernetes pods visible. Because a real Workload
// API socket is available here, spiffe mode fetches a JWT-SVID directly, with
// no exchange/broker involved at all.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	authModeStatic = "static"
	authModeSPIFFE = "spiffe"

	fraudCheckAudience = "bank-server-fraud-check"
)

func main() {
	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Error running bank-fraud-checker", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	AuthMode         string
	FraudCheckURL    string
	PollInterval     time.Duration
	StaticAPIKey     string
	SpiffeSocketPath string
}

func mustGetEnv(variable string) string {
	v, ok := os.LookupEnv(variable)
	if !ok || v == "" {
		slog.Error("Unset environment variable", "variable", variable)
		os.Exit(1)
	}
	return v
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnv() *Env {
	authMode := getEnvWithDefault("AUTH_MODE", authModeStatic)

	pollIntervalStr := getEnvWithDefault("POLL_INTERVAL", "20s")
	pollInterval, err := time.ParseDuration(pollIntervalStr)
	if err != nil {
		slog.Error("Invalid POLL_INTERVAL", "value", pollIntervalStr, "error", err)
		os.Exit(1)
	}

	env := &Env{
		AuthMode:      authMode,
		FraudCheckURL: mustGetEnv("BANK_SERVER_FRAUD_CHECK_URL"),
		PollInterval:  pollInterval,
	}

	switch authMode {
	case authModeStatic:
		env.StaticAPIKey = mustGetEnv("STATIC_API_KEY")
	case authModeSPIFFE:
		env.SpiffeSocketPath = mustGetEnv("SPIFFE_ENDPOINT_SOCKET")
	default:
		slog.Error("Invalid AUTH_MODE", "value", authMode)
		os.Exit(1)
	}

	return env
}

// authMethodLabel names the credential this workload presents to bank-server,
// for logging — matches the auth_method vocabulary the rest of the demo's
// workloads use ("static-secret"/"mtls"/"jwt-svid"/"delegated-jwt").
func authMethodLabel(authMode string) string {
	if authMode == authModeSPIFFE {
		return "jwt-svid"
	}
	return "static-secret"
}

// tokenFunc returns the current bearer credential to present to bank-server,
// fetching/refreshing a JWT-SVID as needed in spiffe mode.
type tokenFunc func(ctx context.Context) (string, error)

func buildTokenFunc(ctx context.Context, env *Env) (tokenFunc, error) {
	switch env.AuthMode {
	case authModeStatic:
		return func(context.Context) (string, error) { return env.StaticAPIKey, nil }, nil
	case authModeSPIFFE:
		source, err := workloadapi.NewJWTSource(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
		if err != nil {
			return nil, fmt.Errorf("unable to create JWT source: %w", err)
		}

		var (
			mu   sync.Mutex
			svid *jwtsvid.SVID
		)
		return func(ctx context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			if svid == nil || time.Until(svid.Expiry) < time.Minute {
				slog.Info("Fetching JWT-SVID", "auth_method", "jwt-svid", "audience", fraudCheckAudience)
				fresh, err := source.FetchJWTSVID(ctx, jwtsvid.Params{Audience: fraudCheckAudience})
				if err != nil {
					return "", fmt.Errorf("failed to fetch JWT-SVID: %w", err)
				}
				svid = fresh
			}
			return svid.Marshal(), nil
		}, nil
	default:
		return nil, fmt.Errorf("invalid AUTH_MODE: %s", env.AuthMode)
	}
}

func run(ctx context.Context, env *Env) error {
	getToken, err := buildTokenFunc(ctx, env)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}

	slog.Info("bank-fraud-checker starting", "auth_method", authMethodLabel(env.AuthMode), "poll_interval", env.PollInterval)

	ticker := time.NewTicker(env.PollInterval)
	defer ticker.Stop()

	for {
		checkOnce(ctx, client, env, getToken)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// transaction mirrors just the fields of bank-server's Transaction this
// workload cares about — Go's JSON decoding ignores the rest.
type transaction struct {
	ID           int  `json:"id"`
	FraudChecked bool `json:"fraudChecked"`
}

type summary struct {
	Transactions []transaction `json:"transactions"`
}

func checkOnce(ctx context.Context, client *http.Client, env *Env, getToken tokenFunc) {
	token, err := getToken(ctx)
	if err != nil {
		slog.Error("Failed to obtain credential", "auth_method", authMethodLabel(env.AuthMode), "error", err)
		return
	}

	unchecked, err := listUnchecked(ctx, client, env.FraudCheckURL, token)
	if err != nil {
		slog.Error("Failed to list transactions", "auth_method", authMethodLabel(env.AuthMode), "error", err)
		return
	}
	slog.Info("Listed transactions", "auth_method", authMethodLabel(env.AuthMode), "unchecked_count", unchecked)

	if unchecked == 0 {
		return
	}

	checked, err := markChecked(ctx, client, env.FraudCheckURL, token)
	if err != nil {
		slog.Error("Failed to mark transactions as fraud-checked", "auth_method", authMethodLabel(env.AuthMode), "error", err)
		return
	}
	slog.Info("Marked transactions as fraud-checked", "auth_method", authMethodLabel(env.AuthMode), "checked_count", checked)
}

func listUnchecked(ctx context.Context, client *http.Client, url, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var s summary
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return 0, fmt.Errorf("failed to decode summary: %w", err)
	}

	count := 0
	for _, t := range s.Transactions {
		if !t.FraudChecked {
			count++
		}
	}
	return count, nil
}

func markChecked(ctx context.Context, client *http.Client, url, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Checked int `json:"checked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}
	return result.Checked, nil
}

package capital

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	cofideclient "github.com/cofide/cofide-sdk-go/http/client"
	cofideserver "github.com/cofide/cofide-sdk-go/http/server"
	"github.com/cofide/cofide-sdk-go/pkg/id"
)

type Poster interface {
	Post(url, contentType string, body io.Reader) (*http.Response, error)
}

type apiKeyTransport struct {
	key  string
	next http.RoundTripper
}

func (t apiKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.key)
	return t.next.RoundTrip(req)
}

func NewServiceClient(ctx context.Context, keyEnv string) (Poster, error) {
	if IsV2() {
		opts := []cofideclient.ClientOption{
			cofideclient.WithContext(ctx),
			cofideclient.WithSPIREAddress(Env("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock")),
		}
		if xdsURI := Env("XDS_SERVER_URI", ""); xdsURI != "" {
			opts = append(opts, cofideclient.WithXDS(xdsURI), cofideclient.WithXDSNodeID(Env("XDS_NODE_ID", "node")))
		}
		return cofideclient.NewClient(opts...)
	}

	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: apiKeyTransport{
			key:  APIKey(keyEnv),
			next: http.DefaultTransport,
		},
	}, nil
}

func PostJSON(ctx context.Context, client Poster, url string, in any, out any) error {
	_ = ctx
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s failed with status %d: %s", url, resp.StatusCode, respBody)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func APIKeyMiddleware(keyEnv string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + APIKey(keyEnv)
		if r.Header.Get("Authorization") != expected {
			http.Error(w, "invalid API key", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Serve(ctx context.Context, service, addr, expectedServiceAccount string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	if IsV2() {
		wrapped := cofideserver.NewServer(
			server,
			cofideserver.WithContext(ctx),
			cofideserver.WithSPIREAddress(Env("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock")),
			cofideserver.WithSVIDMatch(id.Equals("sa", expectedServiceAccount)),
		)
		slog.Info("serving with Cofide mTLS", "service", service, "addr", addr, "expected_sa", expectedServiceAccount)
		return wrapped.ListenAndServe()
	}

	slog.Info("serving with API key auth", "service", service, "addr", addr)
	return server.ListenAndServe()
}

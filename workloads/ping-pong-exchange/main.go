package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const (
	ModeClient = "client"
	ModeServer = "server"
	ModeRelay  = "relay"
)

// Env holds configuration loaded from environment variables.
type Env struct {
	ActorSPIFFEID    string
	ClientSPIFFEID   string
	ExchangeURL      string
	ListenAddress    string
	Mode             string
	ServerURL        string
	ServerSPIFFEID   string
	SpiffeSocketPath string
}

var allowedSignatureAlgs = []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512, jose.ES256, jose.ES384, jose.ES512}

// actorClaim represents the RFC 8693 "act" (actor) claim in a delegated token.
type actorClaim struct {
	Sub string `json:"sub"`
}

// tokenClaims extends the standard JWT claims with the RFC 8693 "act" claim.
type tokenClaims struct {
	jwt.Claims
	Act *actorClaim `json:"act,omitempty"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, getEnv()); err != nil {
		log.Fatal(err)
	}
}

// getEnv reads and validates all required environment variables, exiting on error.
func getEnv() *Env {
	host := getEnvWithDefault("PING_PONG_SERVICE_HOST", "ping-pong-server.demo")
	port := getEnvWithDefault("PING_PONG_SERVICE_PORT", "8443")
	env := &Env{
		ActorSPIFFEID:    getEnvWithDefault("ACTOR_SPIFFE_ID", ""),
		ClientSPIFFEID:   getEnvWithDefault("CLIENT_SPIFFE_ID", ""),
		ExchangeURL:      mustGetEnv("EXCHANGE_URL"),
		ListenAddress:    getEnvWithDefault("PING_PONG_SERVER_LISTEN_ADDRESS", ":8443"),
		Mode:             mustGetMode(),
		ServerURL:        fmt.Sprintf("http://%s:%s", host, port),
		ServerSPIFFEID:   getEnvWithDefault("SERVER_SPIFFE_ID", ""),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
	}

	if env.Mode == ModeClient || env.Mode == ModeRelay {
		if _, err := spiffeid.FromString(env.ServerSPIFFEID); err != nil {
			slog.Error("Invalid SERVER_SPIFFE_ID", "error", err)
			os.Exit(1)
		}
	}

	if env.Mode == ModeServer || env.Mode == ModeRelay {
		if _, err := spiffeid.FromString(env.ClientSPIFFEID); err != nil {
			slog.Error("Invalid CLIENT_SPIFFE_ID", "error", err)
			os.Exit(1)
		}
	}

	if env.ActorSPIFFEID != "" {
		if _, err := spiffeid.FromString(env.ActorSPIFFEID); err != nil {
			slog.Error("Invalid ACTOR_SPIFFE_ID", "error", err)
			os.Exit(1)
		}
	}

	return env
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func mustGetMode() string {
	mode := mustGetEnv("PING_PONG_MODE")
	switch mode {
	case ModeClient, ModeServer, ModeRelay:
		return mode
	default:
		slog.Error("Invalid PING_PONG_MODE", "value", mode, "valid", []string{ModeClient, ModeServer, ModeRelay})
		os.Exit(1)
		return ""
	}
}

func mustGetEnv(variable string) string {
	v, ok := os.LookupEnv(variable)
	if !ok || v == "" {
		slog.Error("Unset environment variable", "variable", variable)
		os.Exit(1)
	}
	return v
}

// run initialises shared dependencies (OIDC discovery, workload API) and starts
// the client, server, or relay goroutine(s) depending on env.Mode.
func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()

	httpClient := &http.Client{Timeout: 10 * time.Second}

	slog.Info("Starting", "mode", env.Mode)
	slog.Info("Fetching OIDC discovery document", "issuer", env.ExchangeURL)
	discovery, err := Discover(env.ExchangeURL, httpClient)
	if err != nil {
		return fmt.Errorf("OIDC discovery failed: %w", err)
	}
	slog.Info("OIDC discovery complete", "token_endpoint", discovery.TokenEndpoint, "jwks_uri", discovery.JWKSUri)

	slog.Info("Connecting to SPIFFE workload API", "socket", env.SpiffeSocketPath)
	wlClient, err := workloadapi.New(initCtx, workloadapi.WithAddr(env.SpiffeSocketPath))
	if err != nil {
		return fmt.Errorf("failed to create workload client: %w", err)
	}
	defer func() { _ = wlClient.Close() }()

	exchangeClient := &ExchangeClient{
		tokenURL: discovery.TokenEndpoint,
		client:   httpClient,
	}
	svidSource := &JWTSVIDSource{
		wlClient: wlClient,
		audience: discovery.TokenEndpoint,
	}

	wg := sync.WaitGroup{}

	var client *pingPongClient
	if env.Mode == ModeClient || env.Mode == ModeRelay {
		client = &pingPongClient{
			env:            env,
			svidSource:     svidSource,
			exchangeClient: exchangeClient,
			client:         &http.Client{Timeout: 10 * time.Second},
		}
	}
	if env.Mode == ModeClient {
		wg.Go(func() { client.run(ctx) })
	}

	var serverErr error
	if env.Mode == ModeServer || env.Mode == ModeRelay {
		server := pingPongServer{
			env:              env,
			authorizedClient: spiffeid.RequireFromString(env.ClientSPIFFEID),
			exchangeClient:   exchangeClient,
			svidSource:       svidSource,
			jwksFetcher:      &JWKSFetcher{url: discovery.JWKSUri, client: httpClient},
			client:           client,
		}
		if env.ActorSPIFFEID != "" {
			id := spiffeid.RequireFromString(env.ActorSPIFFEID)
			server.authorizedActor = &id
		}
		wg.Go(func() {
			defer cancel()
			serverErr = server.run(ctx)
		})
	}

	wg.Wait()
	return serverErr
}

// pingPongClient periodically sends a ping to the server, using a token-exchanged
// JWT as its credential.
type pingPongClient struct {
	env            *Env
	svidSource     *JWTSVIDSource
	exchangeClient *ExchangeClient
	client         *http.Client
}

// run loops every 5 seconds: fetches a JWT-SVID, exchanges it for an access
// token, and sends a ping to the server until ctx is cancelled.
func (c *pingPongClient) run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		svid, err := c.svidSource.GetSVID(ctx)
		if err != nil {
			slog.With("error", err).Warn("Failed to obtain SVID")
			continue
		}
		slog.Info("Obtained JWT-SVID", "id", svid.ID.String())

		token, err := c.doTokenExchange(ctx, svid)
		if err != nil {
			slog.With("error", err).Warn("Failed to obtain access token")
			continue
		}
		slog.Info("Obtained access token via token exchange", "audience", c.env.ServerSPIFFEID)

		slog.Info("Sending ping", "url", c.env.ServerURL)
		body, err := c.ping(ctx, token)
		if err != nil {
			slog.Error("Failed to reach server", "error", err)
		} else {
			slog.Info("Received pong", "response", string(body))
		}
	}
}

// doTokenExchange exchanges the given JWT-SVID for an access token scoped to
// the configured server SPIFFE ID.
func (c *pingPongClient) doTokenExchange(ctx context.Context, svid *jwtsvid.SVID) (string, error) {
	exchangeResult, err := c.exchangeClient.Exchange(ctx, ExchangeParams{
		ClientAssertionType: "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe",
		ClientAssertion:     svid.Marshal(),
		SubjectTokenType:    "urn:ietf:params:oauth:token-type:jwt-spiffe",
		SubjectToken:        svid.Marshal(),
		Audience:            c.env.ServerSPIFFEID,
	})
	if err != nil {
		return "", err
	}
	return exchangeResult.Token, nil
}

// ping sends a GET request to the server with the token as a Bearer credential
// and returns the response body.
func (c *pingPongClient) ping(ctx context.Context, clientToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.env.ServerURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", clientToken))

	r, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = r.Body.Close()
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d: %s", r.StatusCode, body)
	}
	return body, nil
}

// pingPongServer validates incoming JWT bearer tokens and responds with a pong.
// When a client is set (relay mode) it instead performs a delegated token exchange
// and forwards the request downstream.
type pingPongServer struct {
	env              *Env
	authorizedClient spiffeid.ID
	authorizedActor  *spiffeid.ID // nil if actor validation not required
	exchangeClient   *ExchangeClient
	svidSource       *JWTSVIDSource
	jwksFetcher      *JWKSFetcher
	client           *pingPongClient
}

// run starts the HTTP server and blocks until ctx is cancelled or a fatal error occurs.
func (s *pingPongServer) run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handler)

	server := &http.Server{
		Addr:              s.env.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}
	}()

	slog.Info("Server listening", "address", s.env.ListenAddress)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

// httpError carries an HTTP status code and message for use in handler responses.
type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return e.message }

// handler authenticates the incoming request and either relays it downstream
// (relay mode) or writes a "pong" response (server mode).
func (s *pingPongServer) handler(w http.ResponseWriter, r *http.Request) {
	subjectID, token, svid, herr := s.authenticate(r)
	if herr != nil {
		w.WriteHeader(herr.status)
		_, _ = w.Write([]byte(herr.message))
		return
	}

	if s.client != nil {
		slog.Info("Received ping from client, forwarding to downstream server", "subject", subjectID, "downstream", s.client.env.ServerURL)
		s.handleRelay(w, r, token, svid)
		return
	}

	slog.Info("Received ping from client", "subject", subjectID)
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("...pong")); err != nil {
		slog.Error("Error writing response", "error", err)
	} else {
		slog.Info("Sent pong to client", "subject", subjectID)
	}
}

// authenticate validates the Bearer token in the request, returning the verified
// subject SPIFFE ID, raw token, and server SVID on success.
func (s *pingPongServer) authenticate(r *http.Request) (spiffeid.ID, string, *jwtsvid.SVID, *httpError) {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " {
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "No token provided by client"}
	}
	token := auth[7:]

	jwks, err := s.jwksFetcher.GetJWKS()
	if err != nil {
		slog.Error("Failed to fetch JWKS", "error", err)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusServiceUnavailable, "Unable to fetch JWKS"}
	}

	tok, err := jwt.ParseSigned(token, allowedSignatureAlgs)
	if err != nil {
		slog.Warn("Failed to parse token", "error", err)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid token"}
	}

	var claims tokenClaims
	if err = tok.Claims(jwks, &claims); err != nil {
		slog.Warn("Failed to verify token", "error", err)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid token"}
	}

	if err = claims.ValidateWithLeeway(jwt.Expected{Time: time.Now()}, 0); err != nil {
		slog.Warn("Token failed time validation", "error", err)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid token"}
	}

	if claims.Subject == "" {
		slog.Warn("Invalid subject in token")
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid subject in token"}
	}

	svid, err := s.svidSource.GetSVID(r.Context())
	if err != nil {
		slog.Error("Failed to obtain SVID", "error", err)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusInternalServerError, "Unable to obtain SVID"}
	}

	if !slices.Contains([]string(claims.Audience), svid.ID.String()) {
		slog.Warn("Invalid audience in token", "audience", claims.Audience, "expected", svid.ID.String())
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid audience in token"}
	}

	subjectID, err := spiffeid.FromString(claims.Subject)
	if err != nil {
		slog.Warn("Invalid subject in request", "subject", claims.Subject)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid subject"}
	}

	if err := spiffeid.MatchID(s.authorizedClient)(subjectID); err != nil {
		slog.Warn("Rejected unauthorized request", "subject", claims.Subject)
		return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid subject"}
	}

	if s.authorizedActor != nil {
		if claims.Act == nil || claims.Act.Sub == "" {
			slog.Warn("Missing act claim in delegated token")
			return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Missing act claim"}
		}
		actorID, err := spiffeid.FromString(claims.Act.Sub)
		if err != nil {
			slog.Warn("Invalid act.sub in token", "act_sub", claims.Act.Sub)
			return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid actor"}
		}
		if err := spiffeid.MatchID(*s.authorizedActor)(actorID); err != nil {
			slog.Warn("Rejected unauthorized actor", "actor", claims.Act.Sub)
			return spiffeid.ID{}, "", nil, &httpError{http.StatusUnauthorized, "Invalid actor"}
		}
		slog.Info("Validated delegated token", "subject", subjectID, "actor", claims.Act.Sub, "audience", svid.ID)
	} else {
		slog.Info("Validated token", "subject", subjectID, "audience", svid.ID)
	}

	return subjectID, token, svid, nil
}

// handleRelay performs a token exchange and forwards the ping to the downstream server.
func (s *pingPongServer) handleRelay(w http.ResponseWriter, r *http.Request, token string, svid *jwtsvid.SVID) {
	slog.Info("Performing delegated token exchange", "actor", svid.ID.String(), "audience", s.env.ServerSPIFFEID)
	exchangeResult, err := s.exchangeClient.Exchange(r.Context(), ExchangeParams{
		ClientAssertionType: "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe",
		ClientAssertion:     svid.Marshal(),
		SubjectTokenType:    "urn:ietf:params:oauth:token-type:access_token",
		SubjectToken:        token,
		ActorTokenType:      "urn:ietf:params:oauth:token-type:jwt-spiffe",
		ActorToken:          svid.Marshal(),
		Audience:            s.env.ServerSPIFFEID,
	})
	if err != nil {
		slog.Error("Failed to obtain delegated access token", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Unable to obtain access token"))
		return
	}
	slog.Info("Obtained delegated access token, forwarding ping to downstream server", "url", s.client.env.ServerURL)

	body, err := s.client.ping(r.Context(), exchangeResult.Token)
	if err != nil {
		slog.Error("Failed to reach downstream server", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Problem reaching downstream server"))
		return
	}
	slog.Info("Received pong from downstream server, relaying to client")

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(body); err != nil {
		slog.Error("Error writing response", "error", err)
	}
}

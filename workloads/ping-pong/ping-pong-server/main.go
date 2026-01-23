package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Metrics counters
var (
	handlerErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "handler_errors",
		Help: "The total number of handler errors",
	})
	svidUpdates = promauto.NewCounter(prometheus.CounterOpts{
		Name: "svid_updates",
		Help: "The total number of SVID updates",
	})
	requestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "requests_total",
		Help: "The total number of requests",
	})
	serverStartTime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "server_start_time",
		Help: "The timestamp when the server started",
	})

	successfulConnections = promauto.NewCounter(prometheus.CounterOpts{
		Name: "requests_success",
		Help: "The total number of successful connections",
	})

	lastX509SourceUpdate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "last_x509_source_update",
		Help: "The timestamp of the last X509Source update",
	})

	svidNotAfter = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "svid_not_after",
		Help: "The timestamp when the current SVID certificate expires (NotAfter)",
	})

	svidURISAN = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "svid_uri_san",
		Help: "The SPIFFE ID URI SAN of the current SVID certificate",
	}, []string{"spiffe_id"})
)

func main() {
	serverStartTime.Set(float64(time.Now().Unix()))

	if err := run(context.Background(), getEnv()); err != nil {
		slog.Error("Error running server", "error", err)
		os.Exit(1)
	}
}

type Env struct {
	Port             string
	MetricsPort      string
	SpiffeSocketPath string
	MetricsEnabled   bool
	// ClientSPIFFEIDs is a collection of allowed SPIFFEIDs of the
	// clients making inbound requests to this server
	ClientSPIFFEIDs string
}

func mustGetEnv(variable string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
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

func getEnvBooleanWithDefault(variable string, defaultValue bool) bool {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Error("Invalid boolean value", "variable", variable, "error", err)
		return defaultValue
	}
	return b
}

func getEnv() *Env {
	return &Env{
		Port:             getEnvWithDefault("PORT", ":8443"),
		MetricsPort:      getEnvWithDefault("METRICS_PORT", ":8080"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
		MetricsEnabled:   getEnvBooleanWithDefault("METRICS_ENABLED", true),
		ClientSPIFFEIDs:  mustGetEnv("CLIENT_SPIFFE_IDS"),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", metricsWrapper(handler))

	runMetrics(env, mux)

	source, err := workloadapi.NewX509Source(ctx,
		workloadapi.WithClientOptions(
			workloadapi.WithAddr(env.SpiffeSocketPath),
		),
	)
	if err != nil {
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()

	runMetricsUpdateWatcher(env, source, ctx)

	// Set initial X509 info in metrics
	lastX509SourceUpdate.Set(float64(time.Now().Unix()))
	if svid, err := source.GetX509SVID(); err == nil && len(svid.Certificates) > 0 {
		svidNotAfter.Set(float64(svid.Certificates[0].NotAfter.Unix()))
		svidURISAN.WithLabelValues(svid.ID.String()).Set(1)
	}

	// Only authorize inbound calls from the expected client SPIFFE IDs
	var clientSPIFFEIDs []spiffeid.ID
	allowedSPIFFEIDs := strings.Split(env.ClientSPIFFEIDs, ",")
	for _, allowedSPIFFEID := range allowedSPIFFEIDs {
		clientSPIFFEID, err := spiffeid.FromString(allowedSPIFFEID)
		if err != nil {
			return fmt.Errorf("failed to parse client SPIFFE ID: %w", err)
		}
		clientSPIFFEIDs = append(clientSPIFFEIDs, clientSPIFFEID)
	}
	slog.Info("Allowed client SPIFFE IDs", "spiffe_ids", clientSPIFFEIDs)
	tlsConfig := tlsconfig.MTLSServerConfig(
		source,
		source,
		tlsconfig.AuthorizeOneOf(clientSPIFFEIDs...),
	)
	server := &http.Server{
		Addr:              env.Port,
		TLSConfig:         tlsConfig,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	slog.Info("Server starting", "port", env.Port)

	if err := server.ListenAndServeTLS("", ""); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

func metricsWrapper(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestsTotal.Inc()
		successfulConnections.Inc()
		next(w, r)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("...pong"))
	if err != nil {
		handlerErrors.Inc()
		slog.Error("Error writing response", "error", err)
		return
	}
}

func runMetrics(env *Env, mux *http.ServeMux) {
	if env.MetricsEnabled {
		// Expose metrics endpoint in both the mTLS server and a default HTTP server
		mux.Handle("/metrics", promhttp.Handler())
		http.Handle("/metrics", promhttp.Handler())
		slog.Info("Metrics enabled, starting server", "port", env.MetricsPort)
		go func() {
			if err := http.ListenAndServe(env.MetricsPort, nil); err != nil {
				slog.Error("Error starting metrics server", "error", err)
			}
		}()

	}

}

func runMetricsUpdateWatcher(env *Env, source *workloadapi.X509Source, ctx context.Context) {
	if env.MetricsEnabled {
		// Monitor SVID updates
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-source.Updated():
					lastX509SourceUpdate.Set(float64(time.Now().Unix()))

					svid, err := source.GetX509SVID()
					if err != nil {
						slog.Error("Error getting X509SVID", "error", err)
						continue
					}
					if len(svid.Certificates) > 0 {
						notAfter := svid.Certificates[0].NotAfter
						svidNotAfter.Set(float64(notAfter.Unix()))
					}
					// Set the SPIFFE ID URI SAN metric
					svidURISAN.WithLabelValues(svid.ID.String()).Set(1)
					svidUpdates.Inc()
				}
			}
		}()
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Metrics counters
var (
	handlerErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "handler_errors",
		Help: "The total number of handler errors",
	})
	svidFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "svid_failures",
		Help: "The total number of SVID failures",
	})
	tlsErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tls_errors",
		Help: "The total number of TLS errors",
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
		Name: "successful_connections",
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
		log.Fatal(err)
	}
}

type Env struct {
	Port             string
	MetricsPort      string
	SpiffeSocketPath string
	MetricsEnabled   bool
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
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	log.Printf("Invalid value for %s, using default: %v", variable, defaultValue)
	return defaultValue
}

func getEnv() *Env {
	return &Env{
		Port:             getEnvWithDefault("PORT", ":8443"),
		MetricsPort:      getEnvWithDefault("METRICS_PORT", ":8080"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
		MetricsEnabled:   getEnvBooleanWithDefault("METRICS_ENABLED", true),
	}
}

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", metricsWrapper(handler))

	if env.MetricsEnabled {
		// Expose metrics endpoint in both the mTLS server and a default HTTP server
		mux.Handle("/metrics", promhttp.Handler())
		http.Handle("/metrics", promhttp.Handler())
		go http.ListenAndServe(env.MetricsPort, nil)

	}

	source, err := workloadapi.NewX509Source(ctx,
		workloadapi.WithClientOptions(
			workloadapi.WithAddr(env.SpiffeSocketPath),
		),
	)
	if err != nil {
		svidFailures.Inc()
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer source.Close()

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
					log.Printf("Error getting X509SVID: %v", err)
					continue
				}
				if len(svid.Certificates) > 0 {
					notAfter := svid.Certificates[0].NotAfter
					svidNotAfter.Set(float64(notAfter.Unix()))
				}
				// Set the SPIFFE ID URI SAN metric
				svidURISAN.WithLabelValues(svid.ID.String()).Set(1)
			}
		}
	}()

	// Set initial X509 info in metrics
	lastX509SourceUpdate.Set(float64(time.Now().Unix()))
	if svid, err := source.GetX509SVID(); err == nil && len(svid.Certificates) > 0 {
		svidNotAfter.Set(float64(svid.Certificates[0].NotAfter.Unix()))
		svidURISAN.WithLabelValues(svid.ID.String()).Set(1)
	}

	tlsConfig := tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeAny())
	server := &http.Server{
		Addr:              env.Port,
		TLSConfig:         tlsConfig,
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 10,
	}

	log.Printf("Server starting on %s with metrics at /metrics on %s", env.Port, env.MetricsPort)

	if err := server.ListenAndServeTLS("", ""); err != nil {
		tlsErrors.Inc()
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
		log.Printf("Error writing response: %v", err)
		return
	}
}

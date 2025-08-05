package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Metrics counters
var (
	pingErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ping_errors",
		Help: "The total number of ping errors",
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
		Help: "The total number of requests sent",
	})
	clientStartTime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "client_start_time",
		Help: "The timestamp when the client started",
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
	clientStartTime.Set(float64(time.Now().Unix()))
	if err := run(context.Background(), getEnv()); err != nil {
		log.Fatal(err)
	}
}

type Env struct {
	ServerAddress    string
	ServerPort       int
	MetricsPort      string
	MetricsEnabled   bool
	SpiffeSocketPath string
}

func getEnvWithDefault(variable string, defaultValue string) string {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}
	return v
}

func getEnvIntWithDefault(variable string, defaultValue int) int {
	v, ok := os.LookupEnv(variable)
	if !ok {
		return defaultValue
	}

	intValue, err := strconv.Atoi(v)
	if err != nil {
		return defaultValue
	}

	return intValue
}

func getEnv() *Env {
	return &Env{
		ServerAddress:    getEnvWithDefault("PING_PONG_SERVICE_HOST", "ping-pong-server.demo"),
		ServerPort:       getEnvIntWithDefault("PING_PONG_SERVICE_PORT", 8443),
		MetricsPort:      getEnvWithDefault("METRICS_PORT", ":8080"),
		SpiffeSocketPath: getEnvWithDefault("SPIFFE_ENDPOINT_SOCKET", "unix:///spiffe-workload-api/spire-agent.sock"),
		MetricsEnabled:   getEnvBooleanWithDefault("METRICS_ENABLED", true),
	}
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

func run(ctx context.Context, env *Env) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create X509Source with a separate context for initialization
	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()

	source, err := workloadapi.NewX509Source(initCtx, workloadapi.WithClientOptions(workloadapi.WithAddr(env.SpiffeSocketPath)))
	if err != nil {
		svidFailures.Inc()
		return fmt.Errorf("unable to obtain SVID: %w", err)
	}
	defer source.Close()

	if env.MetricsEnabled {
		// Expose metrics endpoint
		http.Handle("/metrics", promhttp.Handler())
		go http.ListenAndServe(env.MetricsPort, nil)

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
	}

	tlsConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeAny())
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	log.Printf("Client starting with metrics at /metrics on %s", env.MetricsPort)

	for {
		slog.Info("ping...")
		requestsTotal.Inc()
		if err := ping(client, env.ServerAddress, env.ServerPort); err != nil {
			pingErrors.Inc()
			slog.Error("problem reaching server", "error", err)
		} else {
			successfulConnections.Inc()
		}
		time.Sleep(5 * time.Second)
	}
}

func ping(client *http.Client, serverAddr string, serverPort int) error {
	r, err := client.Get((&url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s:%d", serverAddr, serverPort),
	}).String())

	if err != nil {
		return err
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	slog.Info(string(body))
	return nil
}

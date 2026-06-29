package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cofide/cofide-demos/workloads/cofide-capital/internal/capital"
)

//go:embed templates/index.html
var templateFS embed.FS

type server struct {
	paymentsClient capital.Poster
	paymentsURL    string
	templates      *template.Template
}

func main() {
	setupLogging()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	paymentsClient, err := capital.NewServiceClient(ctx, "PAYMENTS_API_KEY")
	if err != nil {
		slog.Error("failed to create payments client", "error", err)
		os.Exit(1)
	}

	s := &server{
		paymentsClient: paymentsClient,
		paymentsURL:    capital.Env("PAYMENTS_URL", "http://payments:8080"),
		templates: template.Must(template.New("index.html").Funcs(template.FuncMap{
			"money": func(cents int64) string {
				return fmt.Sprintf("%.2f", float64(cents)/100)
			},
		}).ParseFS(templateFS, "templates/index.html")),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /demo/events", s.events)
	mux.HandleFunc("POST /payments", s.sendPayment)
	mux.HandleFunc("POST /demo/attack", s.simulateAttack)

	addr := capital.Env("FRONTEND_ADDR", ":8080")
	slog.Info("serving frontend", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("frontend stopped", "error", err)
		os.Exit(1)
	}
}

func setupLogging() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
}

func (s *server) index(w http.ResponseWriter, _ *http.Request) {
	data := map[string]any{
		"Version":  capital.Version(),
		"Accounts": capital.DemoAccounts(),
	}
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) sendPayment(w http.ResponseWriter, r *http.Request) {
	var result capital.PaymentResult
	if err := capital.PostJSON(r.Context(), s.paymentsClient, s.paymentsURL+"/payments", capital.DemoPayment(), &result); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *server) simulateAttack(w http.ResponseWriter, r *http.Request) {
	req := capital.DemoPayment()
	req.Reference = "Simulated stolen credential attack"

	if capital.IsV2() {
		err := postWithStolenKey(r.Context(), s.paymentsURL+"/payments", req)
		capital.PublishEvent(r.Context(), "frontend", capital.EventAttackRejected, map[string]any{
			"attack": "plain HTTPS request without client certificate",
			"error":  fmt.Sprintf("%v", err),
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"v1": "stolen API key would be accepted in the baseline",
			"v2": "rejected before application code handled the request",
		})
		return
	}

	err := postWithStolenKey(r.Context(), s.paymentsURL+"/payments", req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	capital.PublishEvent(r.Context(), "frontend", capital.EventAttackSucceeded, map[string]any{
		"attack":     "stolen copy of PAYMENTS_API_KEY",
		"payment_id": req.PaymentID,
		"amount":     req.Amount,
	})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"v1": "stolen API key accepted",
		"v2": "switch to v2 and run again to see TLS rejection",
	})
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events, err := capital.SubscribeEvents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	flusher, _ := w.(http.Flusher)
	for event := range events {
		payload, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func postWithStolenKey(ctx context.Context, url string, req capital.PaymentRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+capital.APIKey("PAYMENTS_API_KEY"))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("attack request failed with status %d", resp.StatusCode)
	}
	return nil
}

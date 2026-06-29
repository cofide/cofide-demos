package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cofide/cofide-demos/workloads/cofide-capital/internal/capital"
)

type server struct {
	ledgerClient capital.Poster
	ledgerURL    string
}

func main() {
	setupLogging()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ledgerClient, err := capital.NewServiceClient(ctx, "LEDGER_API_KEY")
	if err != nil {
		slog.Error("failed to create ledger client", "error", err)
		os.Exit(1)
	}

	s := &server{
		ledgerClient: ledgerClient,
		ledgerURL:    capital.Env("LEDGER_URL", "http://ledger:8080"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("POST /payments", inbound(http.HandlerFunc(s.createPayment)))
	mux.Handle("GET /accounts/{account_id}/history", inbound(http.HandlerFunc(s.accountHistory)))

	if err := capital.Serve(ctx, "payments", capital.Env("PAYMENTS_ADDR", ":8080"), "frontend", mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("payments stopped", "error", err)
		os.Exit(1)
	}
}

func setupLogging() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
}

func inbound(next http.Handler) http.Handler {
	if capital.IsV2() {
		return next
	}
	return capital.APIKeyMiddleware("PAYMENTS_API_KEY", next)
}

func (s *server) createPayment(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var req capital.PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.PaymentID == "" {
		req.PaymentID = capital.NewPaymentID()
	}

	capital.PublishEvent(r.Context(), "payments", capital.EventPaymentInitiated, map[string]any{
		"payment_id":   req.PaymentID,
		"from_account": req.FromAccount,
		"to_account":   req.ToAccount,
		"amount":       req.Amount,
	})

	var record capital.LedgerRecord
	if err := capital.PostJSON(r.Context(), s.ledgerClient, s.ledgerURL+"/records", req, &record); err != nil {
		slog.Error("ledger write failed", "error", err)
		http.Error(w, fmt.Sprintf("ledger write failed: %v", err), http.StatusBadGateway)
		return
	}

	if capital.IsV2() {
		capital.PublishEvent(r.Context(), "payments", capital.EventMTLSOK, map[string]any{
			"src_spiffe_id": "spiffe://cofide-capital/frontend",
			"dst_spiffe_id": "spiffe://cofide-capital/payments",
		})
	}
	capital.PublishEvent(r.Context(), "payments", capital.EventPaymentApproved, map[string]any{
		"payment_id": req.PaymentID,
		"status":     "approved",
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(capital.PaymentResult{
		PaymentID: req.PaymentID,
		Status:    "approved",
		Message:   "Payment approved and written to ledger",
	})
}

func (s *server) accountHistory(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("account_id")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"account_id": accountID,
		"history": []map[string]any{
			{"counterparty": "ACC-0002", "amount": 12000, "age_days": 12},
			{"counterparty": "ACC-0002", "amount": 8400, "age_days": 7},
		},
	})
}

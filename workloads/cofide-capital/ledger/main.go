package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cofide/cofide-demos/workloads/cofide-capital/internal/capital"
)

type ledgerStore struct {
	mu      sync.Mutex
	records []capital.LedgerRecord
}

func main() {
	setupLogging()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := &ledgerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("POST /records", inbound(http.HandlerFunc(store.createRecord)))
	mux.HandleFunc("GET /records", store.listRecords)

	if err := capital.Serve(ctx, "ledger", capital.Env("LEDGER_ADDR", ":8080"), "payments", mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("ledger stopped", "error", err)
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
	return capital.APIKeyMiddleware("LEDGER_API_KEY", next)
}

func (s *ledgerStore) createRecord(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var req capital.PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	record := capital.LedgerRecord{
		PaymentID:   req.PaymentID,
		FromAccount: req.FromAccount,
		ToAccount:   req.ToAccount,
		Amount:      req.Amount,
		Status:      "written",
		CreatedAt:   time.Now().UTC(),
	}
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()

	if capital.IsV2() {
		capital.PublishEvent(r.Context(), "ledger", capital.EventMTLSOK, map[string]any{
			"src_spiffe_id": "spiffe://cofide-capital/payments",
			"dst_spiffe_id": "spiffe://cofide-capital/ledger",
		})
	}
	capital.PublishEvent(r.Context(), "ledger", capital.EventPaymentWritten, map[string]any{
		"payment_id": req.PaymentID,
		"amount":     req.Amount,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}

func (s *ledgerStore) listRecords(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.records)
}

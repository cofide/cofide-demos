package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cofide/cofide-demos/workloads/cofide-capital/internal/capital"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := capital.NewServiceClient(ctx, "PAYMENTS_API_KEY")
	if err != nil {
		slog.Error("failed to create payments client", "error", err)
		os.Exit(1)
	}

	paymentsURL := capital.Env("PAYMENTS_URL", "http://payments:8080")
	interval := time.Duration(capital.EnvInt("LOADGEN_INTERVAL_SECONDS", 10)) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req := capital.PaymentRequest{
				PaymentID:   capital.NewPaymentID(),
				FromAccount: "ACC-0001",
				ToAccount:   "ACC-0002",
				Amount:      12500,
				Reference:   "Background demo payment",
			}
			var result capital.PaymentResult
			if err := capital.PostJSON(ctx, client, paymentsURL+"/payments", req, &result); err != nil {
				slog.Warn("loadgen payment failed", "error", err)
				continue
			}
			slog.Info("loadgen payment sent", "payment_id", result.PaymentID, "status", result.Status)
		}
	}
}

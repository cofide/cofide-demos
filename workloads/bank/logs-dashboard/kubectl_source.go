package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// runKubectlTail runs "kubectl logs -f" for the given deployment and publishes
// each line to h, forever. deploy/<name> (not a specific pod) is used
// deliberately: bank-client/bank-server get rolled by toggle-spiffe.sh/
// deploy-static.sh mid-demo, and kubectl logs against a deployment follows
// whichever pod is currently live rather than exiting when the old one is
// replaced. If the process does exit (e.g. no pod is ready yet, or briefly
// during a rollout), it's restarted after a short delay rather than leaving
// that panel dead for the rest of the demo.
func runKubectlTail(ctx context.Context, h *hub, kubeContext, namespace, deployment string) {
	for {
		if ctx.Err() != nil {
			return
		}
		h.publish(fmt.Sprintf("--- tailing deploy/%s (context=%s namespace=%s) ---", deployment, kubeContext, namespace))

		args := []string{"logs", "-f", "--tail=100", "deploy/" + deployment, "-n", namespace}
		if kubeContext != "" {
			args = append([]string{"--context", kubeContext}, args...)
		}
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			h.publish(fmt.Sprintf("--- failed to start kubectl: %s ---", err))
			sleepOrDone(ctx, 5*time.Second)
			continue
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			h.publish(fmt.Sprintf("--- failed to start kubectl: %s ---", err))
			sleepOrDone(ctx, 5*time.Second)
			continue
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			h.publish(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			h.publish(fmt.Sprintf("--- error reading kubectl output: %s ---", err))
		}

		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			slog.Warn("kubectl logs exited, retrying", "deployment", deployment, "error", err)
			h.publish(fmt.Sprintf("--- kubectl logs exited (%s), reconnecting in 3s ---", err))
		}
		sleepOrDone(ctx, 3*time.Second)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

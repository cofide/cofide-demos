// Command logs-dashboard runs a small local web page that tails bank-client's
// and bank-server's kubectl logs alongside bank-lambda's and bank-agent's
// CloudWatch logs, side by side, so a presenter doesn't have to switch
// between terminal tabs and the CloudWatch console mid-demo.
//
// It reads live infrastructure (this cluster's kubectl context, this AWS
// account's CloudWatch logs) but writes nothing and only ever calls read-only
// APIs ("kubectl logs", "logs:FilterLogEvents", "logs:DescribeLogGroups").
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

//go:embed static/index.html
var staticFS embed.FS

func main() {
	kubeContext := flag.String("kube-context", "", "kubectl context to use (default: kubectl's current context)")
	namespace := flag.String("namespace", "bank", "Kubernetes namespace bank-client/bank-server run in")
	clientDeployment := flag.String("bank-client-deployment", "bank-client", "bank-client Deployment name")
	serverDeployment := flag.String("bank-server-deployment", "bank-server", "bank-server Deployment name")
	lambdaFunctionName := flag.String("lambda-function-name", "cofide-bank-demo-lambda", "bank-lambda function name (its CloudWatch log group is /aws/lambda/<this>)")
	agentRuntimeName := flag.String("agent-runtime-name", "cofide_bank_demo_agent", "bank-agent's AgentCore Runtime name, used to discover its CloudWatch log group (ignored if --agent-log-group is set)")
	agentLogGroup := flag.String("agent-log-group", "", "bank-agent's CloudWatch log group name, overriding auto-discovery via --agent-runtime-name")
	awsRegion := flag.String("aws-region", "", "AWS region (default: your AWS CLI/environment default)")
	pollInterval := flag.Duration("poll-interval", 3*time.Second, "how often to poll CloudWatch Logs for new events")
	addr := flag.String("addr", "localhost:8090", "address to serve the dashboard on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	awsCfgOpts := []func(*config.LoadOptions) error{}
	if *awsRegion != "" {
		awsCfgOpts = append(awsCfgOpts, config.WithRegion(*awsRegion))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, awsCfgOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load AWS config: %s\n", err)
		os.Exit(1)
	}
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)

	resolvedAgentLogGroup := *agentLogGroup
	if resolvedAgentLogGroup == "" {
		resolvedAgentLogGroup, err = discoverAgentLogGroup(ctx, cwClient, *agentRuntimeName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not discover bank-agent's log group: %s\n", err)
			os.Exit(1)
		}
		slog.Info("discovered bank-agent log group", "log_group", resolvedAgentLogGroup)
	}

	hubs := map[string]*hub{
		"bank-client": newHub(),
		"bank-server": newHub(),
		"lambda":      newHub(),
		"agent":       newHub(),
	}

	go runKubectlTail(ctx, hubs["bank-client"], *kubeContext, *namespace, *clientDeployment)
	go runKubectlTail(ctx, hubs["bank-server"], *kubeContext, *namespace, *serverDeployment)
	go runCloudWatchTail(ctx, hubs["lambda"], cwClient, "/aws/lambda/"+*lambdaFunctionName, *pollInterval)
	go runCloudWatchTail(ctx, hubs["agent"], cwClient, resolvedAgentLogGroup, *pollInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	for id, h := range hubs {
		mux.HandleFunc("/stream/"+id, sseHandler(h))
	}

	server := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Cofide bank demo logs dashboard: http://%s\n", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %s\n", err)
		os.Exit(1)
	}
}

// sseHandler streams a hub's published lines to a browser client as
// Server-Sent Events, replaying recent history first so a tab opened
// mid-demo isn't blank until the next new line arrives.
func sseHandler(h *hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, history, unsubscribe := h.subscribe()
		defer unsubscribe()

		for _, line := range history {
			writeSSELine(w, line)
		}
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case line := <-ch:
				writeSSELine(w, line)
				flusher.Flush()
			}
		}
	}
}

func writeSSELine(w http.ResponseWriter, line string) {
	for _, part := range splitLines(line) {
		_, _ = fmt.Fprintf(w, "data: %s\n", part)
	}
	_, _ = fmt.Fprint(w, "\n")
}

// splitLines guards against a source producing a line containing an embedded
// newline (SSE's "data:" framing requires one "data:" prefix per line, or the
// event is truncated at the first newline by the browser's EventSource parser).
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

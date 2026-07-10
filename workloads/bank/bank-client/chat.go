package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

type chatRequest struct {
	Question string `json:"question"`
}

// writeChatError writes a JSON body matching the shape the dashboard's chat
// JS expects ({"error": "..."}) — plain http.Error bodies aren't valid JSON,
// which makes the frontend's res.json() throw and mask the real error
// behind a generic "something went wrong reaching the assistant" message.
func writeChatError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// handleChat proxies a signed-in customer's question to bank-agent's
// AgentCore Runtime invoke endpoint, forwarding their OIDC access token as
// the bearer credential. AgentCore Runtime's inbound custom JWT authorizer
// validates that token before bank-agent's code ever runs (see
// terraform/agentcore.tf) — bank-client doesn't need AWS credentials or
// SigV4 signing for this call, since the runtime is configured for JWT
// bearer auth, not IAM.
func handleChat(store *sessionStore, httpClient *http.Client, invokeURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := store.fromRequest(r)
		if err != nil {
			writeChatError(w, http.StatusUnauthorized, "Not signed in")
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Question == "" {
			writeChatError(w, http.StatusBadRequest, "question is required")
			return
		}

		body, err := json.Marshal(map[string]string{"question": req.Question})
		if err != nil {
			writeChatError(w, http.StatusInternalServerError, "Unable to build request")
			return
		}

		agentReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, invokeURL, bytes.NewReader(body))
		if err != nil {
			writeChatError(w, http.StatusInternalServerError, "Unable to reach bank-agent")
			return
		}
		agentReq.Header.Set("Content-Type", "application/json")
		agentReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", sess.IDToken))

		resp, err := httpClient.Do(agentReq)
		if err != nil {
			slog.Error("Failed to reach bank-agent", "error", err)
			writeChatError(w, http.StatusBadGateway, "Unable to reach bank-agent")
			return
		}
		defer func() { _ = resp.Body.Close() }()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			slog.Error("Failed to relay bank-agent response", "error", err)
		}
	}
}

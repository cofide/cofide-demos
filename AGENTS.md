# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

## Commands

```bash
# Lint
just lint

# Build all demos (requires ko and kubectl)
just build-demos

# Build individual demo variants
just build-ping-pong
just build-ping-pong-mesh
just build-ping-pong-cofide
just build-ping-pong-jwt
just build-ping-pong-exchange
just build-aws-oidc
just build-gcp-oidc

# Check required tools are installed
just check-deps
```

Build uses `ko` for multi-platform container images (linux/amd64, linux/arm64). Set `KO_DOCKER_REPO` to push to a registry (defaults to `ko.local`).

There are no unit tests in this repository.

## Architecture

This repo contains demo applications showcasing [Cofide](https://www.cofide.io) and SPIFFE-based identity patterns.

### Demo Variants (`workloads/`)

| Variant | Auth Mechanism | Notes |
|---------|---------------|-------|
| `ping-pong` | SPIFFE mTLS (X.509 SVIDs) | Reference implementation; includes Prometheus metrics |
| `ping-pong-jwt` | SPIFFE JWT-SVIDs over HTTP | Uses Bearer tokens; server returns its own SPIFFE ID |
| `ping-pong-mesh` | Plain HTTP | No auth; intended for use behind a service mesh |
| `ping-pong-cofide` | SPIFFE mTLS via Cofide SDK | Uses `cofide-sdk-go` for XDS service discovery |
| `ping-pong-exchange` | JWT + OAuth 2.0 Token Exchange (RFC 8693) | Single binary; supports `client`, `server`, and `relay` modes |
| `aws-oidc` | SPIFFE + AWS STS OIDC | Demonstrates AWS credential exchange via SPIFFE identity |
| `gcp-oidc` | SPIFFE + GCP WIF | Demonstrates GCP Workload Identity Federation with SPIFFE |
| `bank` | Toggle: static API key vs SPIFFE (X.509-SVID mTLS for the client↔server hop, JWT-SVID minted by Cofide Credex for the Lambda↔server and Agent↔server hops) | Realistic demo (web client, ledger server, AWS Lambda webhook, AWS Bedrock AgentCore chat agent) with a live static-secret/SPIFFE toggle; Helm chart + Terraform instead of raw manifests |

### Key Patterns

**SPIFFE Workload API**: All SPIFFE variants connect to the workload API socket at `/spiffe-workload-api/spire-agent.sock` to fetch SVIDs (X.509 or JWT). The trust domain and SPIFFE IDs are environment-driven.

**ping-pong (mTLS)**: Server validates client SPIFFE IDs against `CLIENT_SPIFFE_IDS` env var. Exposes mTLS on `:8443` and Prometheus metrics on `:8080`.

**ping-pong-jwt**: Client fetches JWT-SVID from workload API and sends as `Authorization: Bearer` header. Server validates via workload API, responds with its own SPIFFE ID.

**ping-pong-cofide**: Wraps mTLS with `cofide-sdk-go/http/server` and `cofide-sdk-go/http/client`. Supports XDS-based service discovery configured via environment variables.

**ping-pong-exchange**: Single binary controlled by `PING_PONG_MODE` (`client`/`server`/`relay`). On startup, fetches the OIDC discovery document from `EXCHANGE_URL` to locate the token and JWKS endpoints. Clients exchange their JWT-SVID for an audience-scoped access token (RFC 8693) and send it as a `Bearer` credential. The server validates the token signature via JWKS, checks audience/subject, and optionally enforces an `act` claim for delegated (relay) tokens.

**bank**: Realistic demo — `bank-client` (web dashboard), `bank-server` (in-memory ledger, two listeners — a client-facing one, and one shared by `bank-lambda`/`bank-agent` on different routes since both are AWS-hosted callers with no mTLS), `bank-lambda` (AWS Lambda webhook), `bank-agent` (AWS Bedrock AgentCore spending-insights chat agent) — built to resonate with non-technical audiences rather than just proving connectivity. Every hop is controlled by an `AUTH_MODE` env var (`static` or `spiffe`), always presented as `Authorization: Bearer <token>` regardless of mode so handler logic stays uniform: in `static` mode the bearer value is a pre-shared API key (constant-time compared); in `spiffe` mode it's validated as a SPIFFE credential instead (X.509-SVID mTLS for `bank-client`→`bank-server`, and a JWT-SVID for `bank-lambda`→`bank-server`, obtained by the Lambda exchanging its AWS web identity token with **Cofide Credex**). `bank-agent` answers a signed-in customer's questions by calling `bank-server`, authenticating with a static key (plus an asserted `X-On-Behalf-Of` header) in `static` mode, or via a Credex-minted *delegated* token (customer as subject, `bank-agent` as actor, RFC 8693) obtained through AWS Bedrock AgentCore Identity's own On-Behalf-Of token exchange in `spiffe` mode — see `workloads/bank/docs/agentcore-identity.md` for why Credex is still required even though AgentCore has native identity features, and what's unconfirmed about that integration. Signing in to chat (via any OIDC-compliant IdP — Ory, Auth0, Okta, etc.; `bank-client` is registered as a public OAuth2 client using PKCE, not a client secret, consistent with this demo's move away from static secrets) is orthogonal to `AUTH_MODE` — a customer logs in the same way in both modes; only how `bank-agent` proves its own identity to `bank-server` afterwards changes. `bank-agent` is a core part of the demo (its Terraform is unconditional, like the Lambda's), not an opt-in extra, but it has an inherent bootstrap-ordering requirement: its container image must exist in ECR (a separate `terraform/bootstrap` root module, applied first) before its Agent Runtime can be created. Switching modes is a `helm upgrade --set authMode=...` + rollout restart (plus matching Terraform `auth_mode` variables for the Lambda and, if deployed, the agent) — not a runtime hot-reload, since the SPIFFE Workload API socket must be mounted at pod start. This is the pattern to reuse for any future demo that needs a live static-secret/SPIFFE toggle. Unlike the other demos, `bank`'s Kubernetes manifests are a Helm chart (`workloads/bank/chart/bank`) rather than raw YAML + `envsubst`, and its Lambda and agent are provisioned via Terraform (`workloads/bank/terraform`) rather than `ko`/`build-demos` — the Lambda packaged directly from `bank-lambda/handler.py`, the agent as a container image pushed to ECR via `scripts/build-bank-agent.sh` (AgentCore Runtime requires ARM64 images, unlike `ko`'s multi-platform output).

### Key Dependencies

- `github.com/spiffe/go-spiffe/v2` — SPIFFE identity, X.509/JWT SVIDs, mTLS
- `github.com/cofide/cofide-sdk-go` — Cofide HTTP wrappers, XDS integration
- `github.com/go-jose/go-jose/v4` — JWT/JOSE operations (used in exchange variant)
- `github.com/prometheus/client_golang` — Metrics (ping-pong only)
- `github.com/gin-gonic/gin` — HTTP framework (aws-oidc and gcp-oidc only)
- `github.com/aws/aws-sdk-go-v2` — AWS SDK (aws-oidc only)
- `cloud.google.com/go/storage` — GCS (gcp-oidc only)

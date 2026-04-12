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
just build-aws-oidc

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
| `aws-oidc` | SPIFFE + AWS STS OIDC | Demonstrates AWS credential exchange via SPIFFE identity |

### Key Patterns

**SPIFFE Workload API**: All SPIFFE variants connect to the workload API socket at `/spiffe-workload-api/spire-agent.sock` to fetch SVIDs (X.509 or JWT). The trust domain and SPIFFE IDs are environment-driven.

**ping-pong (mTLS)**: Server validates client SPIFFE IDs against `CLIENT_SPIFFE_IDS` env var. Exposes mTLS on `:8443` and Prometheus metrics on `:8080`.

**ping-pong-jwt**: Client fetches JWT-SVID from workload API and sends as `Authorization: Bearer` header. Server validates via workload API, responds with its own SPIFFE ID.

**ping-pong-cofide**: Wraps mTLS with `cofide-sdk-go/http/server` and `cofide-sdk-go/http/client`. Supports XDS-based service discovery configured via environment variables.

### Key Dependencies

- `github.com/spiffe/go-spiffe/v2` — SPIFFE identity, X.509/JWT SVIDs, mTLS
- `github.com/cofide/cofide-sdk-go` — Cofide HTTP wrappers, XDS integration
- `github.com/go-jose/go-jose/v4` — JWT/JOSE operations (used in exchange variant)
- `github.com/prometheus/client_golang` — Metrics (ping-pong only)
- `github.com/gin-gonic/gin` — HTTP framework (aws-oidc only)
- `github.com/aws/aws-sdk-go-v2` — AWS SDK (aws-oidc only)

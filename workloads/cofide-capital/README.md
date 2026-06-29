# Cofide Capital

Cofide Capital is an enterprise-oriented demo application for Cofide Connect.
Phase 1 models a simple financial services payment path:

```text
frontend -> payments -> ledger
```

The same business flow runs in two modes:

- `v1`: plain HTTP plus shared API keys stored in Kubernetes Secrets.
- `v2`: Cofide SDK mTLS using SPIFFE X.509-SVIDs.

The frontend includes a small `/demo` control surface with a live event feed,
a deterministic payment trigger, and an attack simulation.

## Phase 1 services

| Service | Purpose |
| --- | --- |
| `frontend` | Bank UI, demo controls, SSE event feed |
| `payments` | Validates payment requests and writes to ledger |
| `ledger` | Append-only in-memory transaction log |
| `loadgen` | Background traffic generator |
| `redis` | In-cluster event bus |

## Deploy

From the repository root:

```bash
just build-cofide-capital
just deploy-cofide-capital-v1
just deploy-cofide-capital-v2
```

`v2` assumes Cofide Connect, the SPIFFE CSI driver, and workload identities are
available in the cluster.

### Kind local image workflow

For local Kind testing without pushing to a registry:

```bash
KIND_CLUSTER_NAME=cofide-capital just build-load-cofide-capital-kind
COFIDE_CAPITAL_NAMESPACE=production just deploy-cofide-capital-v1
kubectl -n production port-forward svc/frontend 8080:80
```

### Cofide Connect bootstrap for v2

The `connect/` directory contains boilerplate adapted from
`cofide/connect-reference/workloads` for a single trust-zone Kind workload
cluster using `cofidectl` and OSS SPIRE.

Create a config file:

```bash
cp workloads/cofide-capital/connect/config.env.example workloads/cofide-capital/connect/config.env
```

Populate the Connect settings, then run:

```bash
workloads/cofide-capital/connect/setup-single-trust-zone-kind.sh
```

The script performs the same high-level steps as the reference architecture:

1. Optionally creates a Kind workload cluster.
2. Creates the workload namespace.
3. Runs `cofidectl connect init`.
4. Adds a trust zone and Kubernetes workload cluster.
5. Adds a Kubernetes namespace attestation policy.
6. Binds that policy to the trust zone.
7. Runs `cofidectl up`.

Once complete, deploy v2 into the attested namespace:

```bash
COFIDE_CAPITAL_NAMESPACE=production just deploy-cofide-capital-v2
kubectl -n production rollout status deploy/frontend
kubectl -n production rollout status deploy/payments
kubectl -n production rollout status deploy/ledger
kubectl -n production rollout status deploy/loadgen
```

## Existing repo patterns used

- `workloads/ping-pong-cofide`: Cofide Go SDK mTLS client/server shape.
- `workloads/ping-pong-exchange`: Phase 3 RFC 8693 reference.
- `workloads/aws-oidc`: Phase 2 AWS WIF reference.

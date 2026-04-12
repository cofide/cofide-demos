# ping-pong-jwt

Demonstrates mutual workload authentication using SPIFFE JWT-SVIDs over plain HTTP(S), without mTLS.

## What it demonstrates

Instead of TLS certificates, each side proves its identity using a JWT-SVID — a short-lived, signed JWT whose subject is a SPIFFE ID. Authentication is mutual:

- The **client** fetches a JWT-SVID (audience `ping-pong-server`) from the Workload API and sends it as an `Authorization: Bearer` header.
- The **server** validates the token via the Workload API (`ValidateJWTSVID`), checks the subject against the expected client SPIFFE ID, then fetches its own JWT-SVID (audience `ping-pong-client`) and returns it in the response `Authorization` header.
- The **client** validates the server's token and checks it against the expected server SPIFFE ID before accepting the response.

This pattern shows that cryptographic workload identity doesn't require mTLS — JWT-SVIDs can be used in any HTTP-based protocol that supports bearer tokens.

## Configuration

### Server

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CLIENT_SPIFFE_ID` | Yes | — | SPIFFE ID of the authorised client (e.g. `spiffe://example.org/client`) |
| `PING_PONG_SERVER_LISTEN_ADDRESS` | No | `:8443` | Listen address |
| `SPIFFE_ENDPOINT_SOCKET` | No | `unix:///spiffe-workload-api/spire-agent.sock` | SPIFFE Workload API socket path |

### Client

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `SERVER_SPIFFE_ID` | Yes | — | Expected SPIFFE ID of the server (e.g. `spiffe://example.org/server`) |
| `PING_PONG_SERVICE_HOST` | No | `ping-pong-server.demo` | Server hostname |
| `PING_PONG_SERVICE_PORT` | No | `8443` | Server port |
| `SPIFFE_ENDPOINT_SOCKET` | No | `unix:///spiffe-workload-api/spire-agent.sock` | SPIFFE Workload API socket path |

## Deployment

```bash
export IMAGE_TAG=latest
export CLIENT_SPIFFE_ID=spiffe://example.org/ns/demo/sa/ping-pong-client
export SERVER_SPIFFE_ID=spiffe://example.org/ns/demo/sa/ping-pong-server
export PING_PONG_SERVER_SERVICE_HOST=ping-pong-server.demo
export PING_PONG_SERVER_SERVICE_PORT=8443

envsubst < ping-pong-jwt-server/deploy.yaml | kubectl apply -f -
envsubst < ping-pong-jwt-client/deploy.yaml | kubectl apply -f -
```

The manifests mount the SPIFFE Workload API socket via the `csi.spiffe.io` CSI driver. The server is exposed as a `LoadBalancer` service on port 8443.

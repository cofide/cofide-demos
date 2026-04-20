# Cofide Demos

This repository has example applications that are used to demonstrate Cofide's open source tools, including `cofidectl`.

The examples include `ping-pong` that can be deployed in a single Cofide trust zone, or federated across trust zones with multiple clusters.

There are several flavours of `ping-pong`:

- [`workloads/ping-pong`](workloads/ping-pong/README.md): SPIFFE mTLS-enabled HTTPS ping pong
- [`workloads/ping-pong-cofide`](workloads/ping-pong-cofide/README.md): SPIFFE mTLS-enabled HTTPS ping pong with the [Cofide Go SDK](https://github.com/cofide/cofide-sdk-go)
- [`workloads/ping-pong-jwt`](workloads/ping-pong-jwt/README.md): SPIFFE JWT-authenticated HTTP ping pong
- [`workloads/ping-pong-mesh`](workloads/ping-pong-mesh/README.md): HTTP ping pong (eg for use with a service mesh)
- [`workloads/ping-pong-exchange`](workloads/ping-pong-exchange/README.md): JWT + OAuth 2.0 token exchange (RFC 8693) ping pong
- [`workloads/aws-oidc`](workloads/aws-oidc/README.md): SPIFFE JWT-SVID to AWS credential exchange via STS OIDC

The Cofide Connect [documentation](https://docs.cofide.dev/workloads/communication-patterns/) contains additional information about the zero-trust communication patterns demonstrated by the examples in this repository.

## Deploy a single trust zone Cofide instance

See the [`cofidectl` docs](https://github.com/cofide/cofidectl?tab=readme-ov-file#quickstart)

### Deploy an additional Cofide trust zone instance and federate the workloads

See the [`cofidectl` docs](https://github.com/cofide/cofidectl/blob/main/docs/multi-tz-federation.md)

## Local development deployment

Local development uses `ko` and tags built images under `ko.local/` namespace.

In all the examples, use the following values instead of `ghcr.io` ones:

```
export COFIDE_DEMOS_IMAGE_PREFIX=ko.local/
export COFIDE_DEMOS_IMAGE_PULL_POLICY=Never
```

### Building just one arch

Set `COFIDE_DEMOS_PLATFORMS` to one of the supported platforms, e.g.:

```
export COFIDE_DEMOS_PLATFORMS=linux/amd64
```

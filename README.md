# Cofide Demos

This repository has example applications that are used to demonstrate Cofide's open source tools, including `cofidectl`.

The examples include `ping-pong` that can be deployed in a single Cofide trust zone, or federated across trust zones with multiple clusters.

There are several flavours of `ping-pong`:

- `workloads/ping-pong`: SPIFFE mTLS-enabled HTTPS ping pong
- `workloads/ping-pong-cofide`: SPIFFE mTLS-enabled HTTPS ping pong with the [Cofide Go SDK](https://github.com/cofide/cofide-sdk-go)
- `workloads/ping-pong-jwt`: SPIFFE JWT-authenticated HTTP ping pong
- `workloads/ping-pong-mesh`: HTTP ping pong (eg for use with a service mesh)

## Deploy a single trust zone Cofide instance

See the [`cofidectl` docs](https://github.com/cofide/cofidectl?tab=readme-ov-file#quickstart)

### Deploy an additional Cofide trust zone instance and federate the workloads

See the [`cofidectl` docs](https://github.com/cofide/cofidectl/blob/main/docs/multi-tz-federation.md)

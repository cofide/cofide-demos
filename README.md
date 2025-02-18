# Cofide Demos

This repository has example applications that are used to demonstrate Cofide's open source tools, including `cofidectl`. 

Images are built on push, and published on tag to:

```
ghcr.io/cofide-demos/<workload-name>:<tag>
```

Use `just` to build images locally and push to local `kind`:

```shell
just build all
```
or a particular workload image:

```shell
just build aws/consumer
```

The image repo name is its path with '/' and '-' replaced with '_'.

The examples include `ping-pong` that can be deployed in a single Cofide trust zone, or federated across trust zones with multiple clusters.

There are two flavours of `ping-pong`:

- `workloads/ping-pong`: SPIFFE mTLS-enabled HTTPS ping pong
- `workloads/ping-pong-mesh`: HTTP ping pong for use with a service mesh

## Deploy a single trust zone Cofide instance

See the [`cofidectl` docs](https://www.github.com/cofide/cofidectl/README.md#quickstart)

### Deploy an additional Cofide trust zone instance and federate the workloads
 
See the [`cofidectl` docs](https://www.github.com/cofide/cofidectl/docs/multi-tz-federation.md)

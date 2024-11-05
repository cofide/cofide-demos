# Cofide Demos

This repository has an example `ping-pong` application used to demonstrate Cofide's open source tools, including `cofidectl` and the Cofide Go SDK. The examples include ping-pong in a single Cofide trust-zone, as well as an example of ping-pong federated across trust-zones with multiple clusters.

## Quickstart

### Deploy a single trust zone Cofide instance

To get started, spin up a local Cofide instance using a `kind` cluster. In this example, we wish to establish a trust-zone `cofide-a` with a trust domain `cofide-a.test`. Ensure your `kind` cluster is ready and specify it's name and context using the CLI flags:

```
cofidectl trust-zone add cofide-a --trust-domain cofide-a.test --kubernetes-cluster kind-user --profile kubernetes --kubernetes-context kind-user
```

In order to issue short-lived identities to the ping-pong server and client applications, we need to define Cofide attestation policy; these describe the properties of the workload(s) that will be attested and issued identities by the Cofide workload identity provider. In this case, we'll use a namespace policy and bind it to the trust-zone:

```
cofidectl attestation-policy add --name namespace-demo namespace --namespace demo
cofidectl attestation-policy-binding add --attestation-policy namespace-demo --trust-zone cofide-a 
```

And that's it for configuration - we're ready to spin up the stack (locally):

```
cofidectl up
```

```
✅ Installed: Installation completed for cofide-a on cluster kind-user
✅ All SPIRE server pods and services are ready for cofide-a in cluster kind-user
✅ Configured: Post-installation configuration completed for cofide-a on cluster kind-user
```

🚀

You can read more details and the the various configuration options in the `cofidectl` [documentation](https://www.github.com/cofide/cofidectl/docs).

#### Deploy the application server and client

Now we can deploy the Go server and client and see how they seamlessly obtain a SPIFFE identity and uses it for mTLS. With the Cofide Go SDK, it's a simple drop-in complement for `net/http` and it integrates with the Cofide SPIRE instance on your behalf. You can even add simple authorization rules based on the SPIFFE ID. In these examples, the ping-pong server will only authorize requests from the ping-pong client.

```
just deploy-ping-pong-cofide kind-user
```

#### Safe and secure ping-pong with Cofide

Take a look at the logs of the client pod and see the mTLS-enabled ping-pong, complete with the client and server SPIFFE IDs 🔐:

```
2024/11/02 15:45:50 INFO ping from spiffe://cofide-a.test/ns/demo/sa/ping-pong-client...
2024/11/02 15:45:50 INFO ...pong from spiffe://cofide-a.test/ns/demo/sa/ping-pong-server
```

### Deploy an additional Cofide trust zone instance and federate the workloads

Now let's add an additional trust-zone (`cofide-b`) in a new `kind` cluster (in our case, `kind-user2`) and a Cofide federation between them:

```
cofidectl trust-zone add cofide-b --trust-domain cofide-b.test --kubernetes-cluster kind-user2 --profile kubernetes --kubernetes-context kind-user2
cofidectl federation add federation --left cofide-a --right cofide-b
cofidectl federation add federation --left cofide-b --right cofide-a
cofidectl attestation-policy-binding add --attestation-policy namespace-demo --trust-zone cofide-a --federates-with cofide-b
cofidectl attestation-policy-binding add --attestation-policy namespace-demo --trust-zone cofide-b --federates-with cofide-a
```

As before, we apply the configuration using the `up` command:

```
cofidectl up
```

`cofidectl` will take care of the federation itself and initial exchange of trust roots. We can now deploy ping-pong, this time using a different `Justfile` recipe: this example will deploy the ping-pong server to `kind-user` and the client to `kind-user2`:

```
just deploy-cofide-ping-pong kind-user kind-user2
```

```
2024/11/02 23:35:18 INFO ping from spiffe://cofide-b.test/ns/demo/sa/ping-pong-client...
2024/11/02 23:35:18 INFO ...pong from spiffe://cofide-a.test/ns/demo/sa/ping-pong-server
```

The trust zones have been successfully federated and the client and server workloads are securely communicating with mTLS 🔐.
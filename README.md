# Cofide Demos

This repository has an example `ping-pong` application used to demonstrate Cofide's open source tools, including `cofidectl` and the Cofide Go SDK.

## Quickstart

### Deploy a local Cofide instance

To get started, spin up a local Cofide instance using a `kind` cluster. In this example, we wish to establish a trust-zone `cofide` with a trust domain `cofide.test`: 

```
$ cofide trust-zone add cofide --trust-domain cofide.test --kubernetes-cluster kind-user --profile kubernetes --kubernetes-context kind-user 
```

In order to issue short-lived identities to the ping-pong server and client applications, we need to define Cofide attestation policy; these describe the properties of the workload(s) that will be attested and issued identities by the Cofide workload identity provider. In this case, we'll use a namespace policy:

```
$ cofide attestation-policy add --name namespace-demo namespace --namespace demo --trust-zone tz 
```

And that's it for configuration - we're ready to spin up the stack (locally):

```
$ cofide up
```

```
✅ Installed: Installation completed for cofide on cluster kind-user
✅ All SPIRE server pods and services are ready for tz in cluster kind-user
✅ Configured: Post-installation configuration completed for tz on cluster kind-user
```

🚀

You can read more details and the the various configuration options in the `cofidectl` [documentation](https://www.github.com/cofide/cofidectl/docs).

### Deploy the application server and client

Now we can deploy the Go server and client and see how they seamlessly obtain a SPIFFE identity and uses it for mTLS. With the Cofide Go SDK, it's a simple drop-in complement for `net/http` and it integrates with the Cofide SPIRE instance on your behalf. You can even add simple authorization rules based on the SPIFFE ID.

```
$ just deploy-cofide-sdk
```

### Safe and secure ping-pong with Cofide

Take a look at the logs of the client pod and see the mTLS-enabled ping-pong, complete with the client and server SPIFFE IDs 🔐:

```
2024/11/02 15:45:50 INFO ping from spiffe://foo.bar/ns/demo/sa/ping-pong-client...
2024/11/02 15:45:50 INFO ...pong from spiffe://foo.bar/ns/demo/sa/ping-pong-server
2024/11/02 15:45:55 INFO ping from spiffe://foo.bar/ns/demo/sa/ping-pong-client...
2024/11/02 15:45:55 INFO ...pong from spiffe://foo.bar/ns/demo/sa/ping-pong-server
```




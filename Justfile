
set export
set shell := ["bash", "-euo", "pipefail", "-c"]

export KO_DOCKER_REPO := env_var_or_default("KO_DOCKER_REPO", "kind.local")
export KIND_CLUSTER_NAME := env_var_or_default("KIND_CLUSTER_NAME", "kind")

lint *args:
    golangci-lint run --show-stats {{args}}

check-deps:
    # Check for demo script dependencies
    for cmd in ko kubectl; do \
        if ! command -v $cmd &> /dev/null; then \
            echo "Error: $cmd is not installed" >&2; \
            exit 1; \
        fi \
    done
    echo "All dependencies installed"

# Build all demo ping-pong applications
build-demos: build-ping-pong build-ping-pong-mesh build-aws-oidc

# Build the ping-pong application
build-ping-pong:
  ko build github.com/cofide/cofide-demos/workloads/ping-pong/server -P
  ko build github.com/cofide/cofide-demos/workloads/ping-pong/client -P

# Build the HTTP ping-pong applications to be deployed in a service mesh
build-ping-pong-mesh:
  ko build github.com/cofide/cofide-demos/workloads/ping-pong-mesh/server -P
  ko build github.com/cofide/cofide-demos/workloads/ping-pong-mesh/client -P

build-aws-oidc:
  ko build github.com/cofide/cofide-demos/workloads/aws-oidc/consumer -P
  ko build github.com/cofide/cofide-demos/workloads/aws-oidc/analysis -P

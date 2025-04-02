
set export
set shell := ["bash", "-euo", "pipefail", "-c"]

export KO_DOCKER_REPO := env_var_or_default("KO_DOCKER_REPO", "kind.local")
export KIND_CLUSTER_NAME := env_var_or_default("KIND_CLUSTER_NAME", "kind")
export RELEASE_TAG := env_var_or_default("RELEASE_TAG", "latest")

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

build-demos: build-ping-pong build-ping-pong-mesh build-ping-pong-cofide build-aws-oidc

build-ping-pong:
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong/ping-pong-server -B -t $RELEASE_TAG
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong/ping-pong-client -B -t $RELEASE_TAG

build-ping-pong-mesh:
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong-mesh/ping-pong-mesh-server -B -t $RELEASE_TAG
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong-mesh/ping-pong-mesh-client -B -t $RELEASE_TAG

build-ping-pong-cofide:
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong-cofide/ping-pong-cofide-server -B -t $RELEASE_TAG
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/ping-pong-cofide/ping-pong-cofide-client -B -t $RELEASE_TAG

build-aws-oidc:
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/aws-oidc/aws-oidc-consumer -B -t $RELEASE_TAG
  ko build --platform=linux/amd64,linux/arm64 github.com/cofide/cofide-demos/workloads/aws-oidc/aws-oidc-analysis -B -t $RELEASE_TAG


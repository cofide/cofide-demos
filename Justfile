
set shell := ["bash", "-euo", "pipefail", "-c"]

export KO_DOCKER_REPO := env("KO_DOCKER_REPO", "ko.local")
export KIND_CLUSTER_NAME := env("KIND_CLUSTER_NAME", "kind")
export RELEASE_TAG := env("RELEASE_TAG", "latest")
export COFIDE_DEMOS_PLATFORMS := env("COFIDE_DEMOS_PLATFORMS", "linux/amd64,linux/arm64")
export COFIDE_CAPITAL_DOCKER_REPO := env("COFIDE_CAPITAL_DOCKER_REPO", "ghcr.io/cofide/cofide-demos/cofide-capital")
export COFIDE_CAPITAL_NAMESPACE := env("COFIDE_CAPITAL_NAMESPACE", "default")

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

build-demos: build-ping-pong build-ping-pong-mesh build-ping-pong-cofide build-aws-oidc build-gcp-oidc build-ping-pong-jwt build-ping-pong-exchange build-cofide-capital

build-ping-pong:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong/ping-pong-server -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong/ping-pong-client -B -t $RELEASE_TAG

build-ping-pong-mesh:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-mesh/ping-pong-mesh-server -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-mesh/ping-pong-mesh-client -B -t $RELEASE_TAG

build-ping-pong-cofide:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-cofide/ping-pong-cofide-server -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-cofide/ping-pong-cofide-client -B -t $RELEASE_TAG

build-ping-pong-jwt:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-jwt/ping-pong-jwt-server -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-jwt/ping-pong-jwt-client -B -t $RELEASE_TAG

build-ping-pong-exchange:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/ping-pong-exchange -B -t $RELEASE_TAG

build-aws-oidc:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/aws-oidc/aws-oidc-consumer -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/aws-oidc/aws-oidc-analysis -B -t $RELEASE_TAG

build-gcp-oidc:
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/gcp-oidc/gcp-oidc-consumer -B -t $RELEASE_TAG
  ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/gcp-oidc/gcp-oidc-analysis -B -t $RELEASE_TAG

build-cofide-capital:
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/cofide-capital/frontend -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/cofide-capital/payments -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/cofide-capital/ledger -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --platform=$COFIDE_DEMOS_PLATFORMS github.com/cofide/cofide-demos/workloads/cofide-capital/loadgen -B -t $RELEASE_TAG

build-cofide-capital-local:
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --local --platform=linux/amd64 --sbom=none github.com/cofide/cofide-demos/workloads/cofide-capital/frontend -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --local --platform=linux/amd64 --sbom=none github.com/cofide/cofide-demos/workloads/cofide-capital/payments -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --local --platform=linux/amd64 --sbom=none github.com/cofide/cofide-demos/workloads/cofide-capital/ledger -B -t $RELEASE_TAG
  KO_DOCKER_REPO=$COFIDE_CAPITAL_DOCKER_REPO ko build --local --platform=linux/amd64 --sbom=none github.com/cofide/cofide-demos/workloads/cofide-capital/loadgen -B -t $RELEASE_TAG

load-cofide-capital-kind:
  kind load docker-image --name $KIND_CLUSTER_NAME $COFIDE_CAPITAL_DOCKER_REPO/frontend:$RELEASE_TAG
  kind load docker-image --name $KIND_CLUSTER_NAME $COFIDE_CAPITAL_DOCKER_REPO/payments:$RELEASE_TAG
  kind load docker-image --name $KIND_CLUSTER_NAME $COFIDE_CAPITAL_DOCKER_REPO/ledger:$RELEASE_TAG
  kind load docker-image --name $KIND_CLUSTER_NAME $COFIDE_CAPITAL_DOCKER_REPO/loadgen:$RELEASE_TAG

build-load-cofide-capital-kind: build-cofide-capital-local load-cofide-capital-kind

deploy-cofide-capital-v1:
  kubectl create namespace $COFIDE_CAPITAL_NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -f workloads/cofide-capital/redis/deploy.yaml
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/frontend/overlays/v1
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/payments/overlays/v1
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/ledger/overlays/v1
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/loadgen/overlays/v1

deploy-cofide-capital-v2:
  kubectl create namespace $COFIDE_CAPITAL_NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -f workloads/cofide-capital/redis/deploy.yaml
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/frontend/overlays/v2
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/payments/overlays/v2
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/ledger/overlays/v2
  kubectl apply -n $COFIDE_CAPITAL_NAMESPACE -k workloads/cofide-capital/loadgen/overlays/v2

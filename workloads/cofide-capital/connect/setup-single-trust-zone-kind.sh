#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/config.env}"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "Missing $CONFIG_FILE. Copy config.env.example to config.env and fill in your Connect settings." >&2
  exit 1
fi

source "$CONFIG_FILE"

for cmd in cofidectl kubectl uuidgen; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
done

if [[ "${CREATE_KIND_CLUSTER:-false}" == "true" ]]; then
  if ! command -v kind >/dev/null 2>&1; then
    echo "CREATE_KIND_CLUSTER=true requires kind" >&2
    exit 1
  fi
  if ! command -v envsubst >/dev/null 2>&1; then
    echo "CREATE_KIND_CLUSTER=true requires envsubst" >&2
    exit 1
  fi
  mkdir -p "$SCRIPT_DIR/generated"
  envsubst < "$SCRIPT_DIR/templates/kind-workload-config.yaml" > "$SCRIPT_DIR/generated/kind-workload-config.yaml"
  kind create cluster --name "$WORKLOAD_K8S_CLUSTER_NAME" --config "$SCRIPT_DIR/generated/kind-workload-config.yaml"
fi

kubectl --context "$WORKLOAD_K8S_CLUSTER_CONTEXT" create namespace "$NAMESPACE" --dry-run=client -o yaml | \
  kubectl --context "$WORKLOAD_K8S_CLUSTER_CONTEXT" apply -f -

rm -f cofide.yaml
cofidectl connect init \
  --connect-url "$CONNECT_URL" \
  --connect-trust-domain "$CONNECT_TRUST_DOMAIN" \
  --connect-bundle-host "$CONNECT_BUNDLE_HOST" \
  --authorization-domain "$AUTHORIZATION_DOMAIN" \
  --authorization-client-id "$AUTHORIZATION_CLIENT_ID" \
  --use-oss-spire

if ! cofidectl connect login --check; then
  cofidectl connect login
fi

cofidectl trust-zone add \
  "$WORKLOAD_TRUST_ZONE" \
  --trust-domain "$WORKLOAD_TRUST_DOMAIN"

cofidectl cluster add \
  "$WORKLOAD_K8S_CLUSTER_NAME" \
  --trust-zone "$WORKLOAD_TRUST_ZONE" \
  --kubernetes-context "$WORKLOAD_K8S_CLUSTER_CONTEXT" \
  --profile kubernetes

cofidectl attestation-policy add kubernetes \
  --name "$NAMESPACE-ns-$WORKLOAD_TRUST_ZONE" \
  --namespace "$NAMESPACE"

cofidectl attestation-policy-binding add \
  --trust-zone "$WORKLOAD_TRUST_ZONE" \
  --attestation-policy "$NAMESPACE-ns-$WORKLOAD_TRUST_ZONE"

cofidectl up --trust-zone "$WORKLOAD_TRUST_ZONE"

cat <<EOF

Cofide Connect workload identity infrastructure is installed.

Next:
  COFIDE_CAPITAL_NAMESPACE=$NAMESPACE KIND_CLUSTER_NAME=$WORKLOAD_K8S_CLUSTER_NAME just build-load-cofide-capital-kind
  COFIDE_CAPITAL_NAMESPACE=$NAMESPACE just deploy-cofide-capital-v2

EOF

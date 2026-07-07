#!/usr/bin/env bash
# Deploy the bank demo (bank-server, bank-client, bank-lambda) in "static"
# auth mode — every hop authenticated with a pre-shared API key. This is the
# "before Cofide Connect" starting point; use toggle-spiffe.sh afterwards to
# flip to SPIFFE-issued identity.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

RELEASE="bank"
NAMESPACE="bank"
KUBE_CONTEXT=""
CHART_DIR="$(resolve_chart_dir)"
TERRAFORM_DIR="$(resolve_terraform_dir)"
IMAGE_PREFIX="ko.local/"
IMAGE_TAG="latest"
IMAGE_PULL_POLICY="Never"
WEBHOOK_SERVICE_TYPE="ClusterIP"
WEBHOOK_NODE_PORT=""
CLIENT_SERVICE_TYPE="ClusterIP"
CLIENT_API_KEY=""
WEBHOOK_API_KEY=""
AWS_REGION=""
FUNCTION_NAME="cofide-bank-demo-lambda"
WEBHOOK_URL=""
KIND_CLUSTER=""
SKIP_HELM=0
SKIP_TERRAFORM=0
SKIP_KIND_LOAD=0

usage() {
  cat <<EOF
Usage: $(basename "$0") --kube-context <name> --client-api-key <key> --webhook-api-key <key> \\
         --aws-region <region> [options]

Required:
  --kube-context <name>        kubectl/helm context to target — always required, so the cluster being
                                deployed to is always explicit rather than whatever's currently active
  --client-api-key <key>       Bearer key bank-client presents to bank-server
  --webhook-api-key <key>      Bearer key bank-lambda presents to bank-server
  --aws-region <region>        AWS region for the Lambda (skip with --skip-terraform)

Options:
  --release <name>             Helm release name (default: ${RELEASE})
  --namespace <ns>             Kubernetes namespace, created automatically if it doesn't exist
                                (default: ${NAMESPACE})
  --chart-dir <path>           Helm chart path (default: ${CHART_DIR})
  --terraform-dir <path>       Terraform config path (default: ${TERRAFORM_DIR})
  --image-prefix <prefix>      Image prefix (default: ${IMAGE_PREFIX})
  --image-tag <tag>            Image tag (default: ${IMAGE_TAG})
  --image-pull-policy <policy> Image pull policy (default: ${IMAGE_PULL_POLICY})
  --webhook-service-type <t>   bank-server-webhook Service type (default: ${WEBHOOK_SERVICE_TYPE}; use
                                LoadBalancer on a real cloud cluster, or NodePort + --webhook-node-port
                                on a local kind cluster fronted by a tunnel, so AWS Lambda can reach it)
  --webhook-node-port <port>   Fixed NodePort for bank-server-webhook (only used when
                                --webhook-service-type is NodePort — must match whatever's tunnelled to
                                your host, e.g. a kind extraPortMappings entry)
  --client-service-type <t>    bank-client Service type (default: ${CLIENT_SERVICE_TYPE})
  --function-name <name>       Lambda function name (default: ${FUNCTION_NAME})
  --webhook-url <url>          bank-server webhook URL for the Lambda; auto-detected from the
                                bank-server-webhook Service if omitted (auto-detection only works for
                                LoadBalancer/ClusterIP — always pass this explicitly for NodePort, since
                                the real reachable URL is your tunnel's public hostname, not the Service)
  --kind-cluster <name>        Load images into this kind cluster before deploying; auto-detected from
                                the current kubectl context (kind-<name>) if omitted. Only applies when
                                --image-prefix is ko.local/
  --skip-helm                  Skip the Helm install/upgrade step
  --skip-terraform             Skip the Terraform apply step
  --skip-kind-load             Don't load images into a kind cluster, even if one is detected
  -h, --help                   Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --release) RELEASE="$2"; shift 2 ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --kube-context) KUBE_CONTEXT="$2"; shift 2 ;;
    --chart-dir) CHART_DIR="$2"; shift 2 ;;
    --terraform-dir) TERRAFORM_DIR="$2"; shift 2 ;;
    --image-prefix) IMAGE_PREFIX="$2"; shift 2 ;;
    --image-tag) IMAGE_TAG="$2"; shift 2 ;;
    --image-pull-policy) IMAGE_PULL_POLICY="$2"; shift 2 ;;
    --webhook-service-type) WEBHOOK_SERVICE_TYPE="$2"; shift 2 ;;
    --webhook-node-port) WEBHOOK_NODE_PORT="$2"; shift 2 ;;
    --client-service-type) CLIENT_SERVICE_TYPE="$2"; shift 2 ;;
    --client-api-key) CLIENT_API_KEY="$2"; shift 2 ;;
    --webhook-api-key) WEBHOOK_API_KEY="$2"; shift 2 ;;
    --aws-region) AWS_REGION="$2"; shift 2 ;;
    --function-name) FUNCTION_NAME="$2"; shift 2 ;;
    --webhook-url) WEBHOOK_URL="$2"; shift 2 ;;
    --kind-cluster) KIND_CLUSTER="$2"; shift 2 ;;
    --skip-helm) SKIP_HELM=1; shift ;;
    --skip-terraform) SKIP_TERRAFORM=1; shift ;;
    --skip-kind-load) SKIP_KIND_LOAD=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

require kube-context "$KUBE_CONTEXT"

if [[ "$SKIP_HELM" -eq 0 ]]; then
  require client-api-key "$CLIENT_API_KEY"
  require webhook-api-key "$WEBHOOK_API_KEY"

  maybe_kind_load "$IMAGE_PREFIX" "$KIND_CLUSTER" "$SKIP_KIND_LOAD" \
    "${IMAGE_PREFIX}bank-server:${IMAGE_TAG}" "${IMAGE_PREFIX}bank-client:${IMAGE_TAG}"

  namespace_args=()
  [[ -n "$NAMESPACE" ]] && namespace_args=(--namespace "$NAMESPACE" --create-namespace)

  echo "==> helm upgrade --install ${RELEASE} (authMode=static, kube-context=${KUBE_CONTEXT})"
  helm upgrade --install "$RELEASE" "$CHART_DIR" \
    "${namespace_args[@]}" \
    --kube-context "$KUBE_CONTEXT" \
    --set authMode=static \
    --set image.prefix="$IMAGE_PREFIX" \
    --set image.tag="$IMAGE_TAG" \
    --set image.pullPolicy="$IMAGE_PULL_POLICY" \
    --set server.webhookServiceType="$WEBHOOK_SERVICE_TYPE" \
    --set server.webhookNodePort="$WEBHOOK_NODE_PORT" \
    --set client.serviceType="$CLIENT_SERVICE_TYPE" \
    --set staticAuth.clientApiKey="$CLIENT_API_KEY" \
    --set staticAuth.webhookApiKey="$WEBHOOK_API_KEY"
else
  echo "==> Skipping Helm (--skip-helm)"
fi

if [[ "$SKIP_TERRAFORM" -eq 0 ]]; then
  require aws-region "$AWS_REGION"
  require webhook-api-key "$WEBHOOK_API_KEY"

  if [[ -z "$WEBHOOK_URL" ]]; then
    echo "==> Detecting bank-server-webhook URL"
    if ! WEBHOOK_URL="$(detect_webhook_url)"; then
      echo "Error: could not auto-detect the webhook URL. Pass --webhook-url explicitly," \
           "or wait for the LoadBalancer to get an external address and re-run." >&2
      exit 1
    fi
    echo "    ${WEBHOOK_URL}"
  fi

  echo "==> terraform apply (auth_mode=static)"
  terraform -chdir="$TERRAFORM_DIR" init -input=false
  terraform -chdir="$TERRAFORM_DIR" apply \
    -var "aws_region=${AWS_REGION}" \
    -var "function_name=${FUNCTION_NAME}" \
    -var "auth_mode=static" \
    -var "bank_server_webhook_url=${WEBHOOK_URL}" \
    -var "static_webhook_api_key=${WEBHOOK_API_KEY}"
else
  echo "==> Skipping Terraform (--skip-terraform)"
fi

port_forward_hint="kubectl --context ${KUBE_CONTEXT}"
[[ -n "$NAMESPACE" ]] && port_forward_hint+=" -n ${NAMESPACE}"
port_forward_hint+=" port-forward svc/bank-client 8080:8080"

cat <<EOF

Done. Next steps:
  - View the dashboard:   ${port_forward_hint}  (then open http://localhost:8080)
  - Simulate a transaction:
      aws lambda invoke --function-name ${FUNCTION_NAME} \\
        --payload '{"merchant": "Rail Delivery Group", "category": "Transport", "amountPence": -3450}' \\
        --cli-binary-format raw-in-base64-out out.json
  - Toggle to SPIFFE:     scripts/toggle-spiffe.sh --help
EOF

#!/usr/bin/env bash
# Toggle a running bank demo from "static" to "spiffe" auth mode: SPIFFE
# X.509-SVID mTLS for bank-client<->bank-server, and a JWT-SVID minted by
# Cofide Credex for bank-lambda->bank-server. Requires the cluster to already
# have Cofide Connect/SPIRE and the csi.spiffe.io CSI driver installed, and a
# reachable Credex instance.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

RELEASE="bank"
NAMESPACE="bank"
KUBE_CONTEXT=""
CHART_DIR="$(resolve_chart_dir)"
TERRAFORM_DIR="$(resolve_terraform_dir)"
SERVER_SPIFFE_ID=""
CLIENT_SPIFFE_ID=""
LAMBDA_SPIFFE_ID=""
CREDEX_URL=""
CREDEX_AUDIENCE="cofide-credex"
AWS_REGION=""
FUNCTION_NAME="cofide-bank-demo-lambda"
WEBHOOK_URL=""
SKIP_HELM=0
SKIP_TERRAFORM=0

usage() {
  cat <<EOF
Usage: $(basename "$0") --kube-context <name> --server-spiffe-id <id> --client-spiffe-id <id> \\
         --lambda-spiffe-id <id> --credex-url <url> --aws-region <region> [options]

Required:
  --kube-context <name>     kubectl/helm context to target — always required, so the cluster being
                             modified is always explicit rather than whatever's currently active
  --server-spiffe-id <id>   SPIFFE ID registered for bank-server, e.g. spiffe://example.org/bank/server
  --client-spiffe-id <id>   SPIFFE ID registered for bank-client, e.g. spiffe://example.org/bank/client
  --lambda-spiffe-id <id>   SPIFFE ID registered for bank-lambda, e.g. spiffe://example.org/bank/lambda
  --credex-url <url>        Cofide Credex token exchange endpoint (skip with --skip-terraform)
  --aws-region <region>     AWS region for the Lambda (skip with --skip-terraform)

Options:
  --release <name>          Helm release name (default: ${RELEASE})
  --namespace <ns>          Kubernetes namespace (default: ${NAMESPACE}) — must match whatever
                             deploy-static.sh deployed into
  --chart-dir <path>        Helm chart path (default: ${CHART_DIR})
  --terraform-dir <path>    Terraform config path (default: ${TERRAFORM_DIR})
  --credex-audience <aud>   Audience requested on the AWS web identity token (default: ${CREDEX_AUDIENCE})
  --function-name <name>    Lambda function name (default: ${FUNCTION_NAME})
  --webhook-url <url>       bank-server webhook URL for the Lambda; auto-detected from the
                             bank-server-webhook Service if omitted
  --skip-helm               Skip the Helm upgrade + rollout restart step
  --skip-terraform          Skip the Terraform apply step
  -h, --help                Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --release) RELEASE="$2"; shift 2 ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --kube-context) KUBE_CONTEXT="$2"; shift 2 ;;
    --chart-dir) CHART_DIR="$2"; shift 2 ;;
    --terraform-dir) TERRAFORM_DIR="$2"; shift 2 ;;
    --server-spiffe-id) SERVER_SPIFFE_ID="$2"; shift 2 ;;
    --client-spiffe-id) CLIENT_SPIFFE_ID="$2"; shift 2 ;;
    --lambda-spiffe-id) LAMBDA_SPIFFE_ID="$2"; shift 2 ;;
    --credex-url) CREDEX_URL="$2"; shift 2 ;;
    --credex-audience) CREDEX_AUDIENCE="$2"; shift 2 ;;
    --aws-region) AWS_REGION="$2"; shift 2 ;;
    --function-name) FUNCTION_NAME="$2"; shift 2 ;;
    --webhook-url) WEBHOOK_URL="$2"; shift 2 ;;
    --skip-helm) SKIP_HELM=1; shift ;;
    --skip-terraform) SKIP_TERRAFORM=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

require kube-context "$KUBE_CONTEXT"

namespace_args=()
[[ -n "$NAMESPACE" ]] && namespace_args=(--namespace "$NAMESPACE")

if [[ "$SKIP_HELM" -eq 0 ]]; then
  require server-spiffe-id "$SERVER_SPIFFE_ID"
  require client-spiffe-id "$CLIENT_SPIFFE_ID"
  require lambda-spiffe-id "$LAMBDA_SPIFFE_ID"

  echo "==> helm upgrade ${RELEASE} (authMode=spiffe, kube-context=${KUBE_CONTEXT})"
  # --reuse-values is essential here: without it, Helm resets every value not
  # re-specified on this command back to the chart's values.yaml defaults —
  # which would silently revert image.*, server.webhookServiceType/
  # webhookNodePort, and client.serviceType set by deploy-static.sh.
  helm upgrade "$RELEASE" "$CHART_DIR" \
    "${namespace_args[@]}" \
    --kube-context "$KUBE_CONTEXT" \
    --reuse-values \
    --set authMode=spiffe \
    --set spiffe.serverSpiffeId="$SERVER_SPIFFE_ID" \
    --set spiffe.clientSpiffeId="$CLIENT_SPIFFE_ID" \
    --set spiffe.lambdaSpiffeId="$LAMBDA_SPIFFE_ID"

  echo "==> kubectl rollout restart"
  kubectl rollout restart "${namespace_args[@]}" --context "$KUBE_CONTEXT" deployment/bank-server deployment/bank-client
  kubectl rollout status "${namespace_args[@]}" --context "$KUBE_CONTEXT" deployment/bank-server
  kubectl rollout status "${namespace_args[@]}" --context "$KUBE_CONTEXT" deployment/bank-client
else
  echo "==> Skipping Helm (--skip-helm)"
fi

if [[ "$SKIP_TERRAFORM" -eq 0 ]]; then
  require credex-url "$CREDEX_URL"
  require aws-region "$AWS_REGION"

  if [[ -z "$WEBHOOK_URL" ]]; then
    echo "==> Detecting bank-server-webhook URL"
    if ! WEBHOOK_URL="$(detect_webhook_url)"; then
      echo "Error: could not auto-detect the webhook URL. Pass --webhook-url explicitly." >&2
      exit 1
    fi
    echo "    ${WEBHOOK_URL}"
  fi

  echo "==> terraform apply (auth_mode=spiffe)"
  terraform -chdir="$TERRAFORM_DIR" init -input=false
  terraform -chdir="$TERRAFORM_DIR" apply \
    -var "aws_region=${AWS_REGION}" \
    -var "function_name=${FUNCTION_NAME}" \
    -var "auth_mode=spiffe" \
    -var "bank_server_webhook_url=${WEBHOOK_URL}" \
    -var "token_exchange_url=${CREDEX_URL}" \
    -var "credex_audience=${CREDEX_AUDIENCE}"
else
  echo "==> Skipping Terraform (--skip-terraform)"
fi

cat <<EOF

Done. Invoke the Lambda again and reload the dashboard — the header badge
should now read "Connected via SPIFFE":
  aws lambda invoke --function-name ${FUNCTION_NAME} \\
    --payload '{"merchant": "Rail Delivery Group", "category": "Transport", "amountPence": -3450}' \\
    --cli-binary-format raw-in-base64-out out.json

Note: this only works if ${SERVER_SPIFFE_ID:-<server-spiffe-id>}, ${CLIENT_SPIFFE_ID:-<client-spiffe-id>}
and ${LAMBDA_SPIFFE_ID:-<lambda-spiffe-id>} are already registered in your trust zone/Credex config —
that registration happens outside this repo.
EOF

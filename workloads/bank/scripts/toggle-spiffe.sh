#!/usr/bin/env bash
# Toggle a running bank demo from "static" to "spiffe" auth mode: SPIFFE
# X.509-SVID mTLS for bank-client<->bank-server, a JWT-SVID minted by Cofide
# Credex for bank-lambda->bank-server, and a JWT-SVID fetched directly from a
# co-located SPIRE agent for bank-fraud-checker->bank-server (no Credex
# involved there — unlike bank-lambda, that workload has real Workload API
# access). Requires the cluster to already have Cofide Connect/SPIRE and the
# csi.spiffe.io CSI driver installed, and a reachable Credex instance.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

RELEASE="bank"
NAMESPACE="bank"
KUBE_CONTEXT=""
CHART_DIR="$(resolve_chart_dir)"
TERRAFORM_DIR="$(resolve_terraform_dir)"
BOOTSTRAP_DIR="$(resolve_bootstrap_dir)"
SERVER_SPIFFE_ID=""
CLIENT_SPIFFE_ID=""
LAMBDA_SPIFFE_ID=""
FRAUD_CHECKER_SPIFFE_ID=""
CREDEX_URL=""
CREDEX_AUDIENCE="bank-server-webhook"
CREDEX_DISCOVERY_URL=""
AWS_REGION=""
FUNCTION_NAME="cofide-bank-demo-lambda"
WEBHOOK_URL=""
IMAGE_PREFIX="ko.local/"
IMAGE_TAG="latest"
KIND_CLUSTER=""
SKIP_HELM=0
SKIP_TERRAFORM=0
SKIP_KIND_LOAD=0

usage() {
  cat <<EOF
Usage: $(basename "$0") --kube-context <name> --server-spiffe-id <id> --client-spiffe-id <id> \\
         --lambda-spiffe-id <id> --fraud-checker-spiffe-id <id> --credex-url <url> --aws-region <region> \\
         [options]

Required:
  --kube-context <name>     kubectl/helm context to target — always required, so the cluster being
                             modified is always explicit rather than whatever's currently active
  --server-spiffe-id <id>   SPIFFE ID registered for bank-server, e.g. spiffe://example.org/bank/server
  --client-spiffe-id <id>   SPIFFE ID registered for bank-client, e.g. spiffe://example.org/bank/client
  --lambda-spiffe-id <id>   SPIFFE ID registered for bank-lambda, e.g. spiffe://example.org/bank/lambda
  --fraud-checker-spiffe-id <id>
                            SPIFFE ID registered for bank-fraud-checker, e.g.
                            spiffe://example.org/vm/bank-fraud-checker — registered against the SPIRE
                            agent co-located with it on its VM (see workloads/bank/README.md), not this
                            cluster's trust domain necessarily, if they differ
  --credex-url <url>        Cofide Credex token exchange endpoint (skip with --skip-terraform)
  --aws-region <region>     AWS region for the Lambda and bank-agent (skip with --skip-terraform)

The OIDC discovery URL and allowed client are auto-detected from terraform/bootstrap's output (same
values deploy-static.sh already used) — not passed as flags here. bank-agent has no SPIFFE identity
(it's an AgentCore Runtime workload, not a k8s pod) — the actor identity bank-server authorizes for it
(AGENT_AUTHORIZED_ACTOR, its IAM execution role ARN) is likewise auto-detected from terraform/'s own
output, not passed as a flag.

Options:
  --release <name>          Helm release name (default: ${RELEASE})
  --namespace <ns>          Kubernetes namespace (default: ${NAMESPACE}) — must match whatever
                             deploy-static.sh deployed into
  --chart-dir <path>        Helm chart path (default: ${CHART_DIR})
  --terraform-dir <path>    Terraform config path (default: ${TERRAFORM_DIR})
  --bootstrap-dir <path>    terraform/bootstrap module path (default: ${BOOTSTRAP_DIR})
  --credex-audience <aud>   Audience requested on the AWS web identity token for bank-lambda's exchange
                             (default: ${CREDEX_AUDIENCE}) — Credex's bespoke exchange mints the
                             resulting JWT-SVID's audience as a pass-through of this value, so it must
                             match bank-server's webhookAudience constant, not just identify Credex
  --function-name <name>    Lambda function name (default: ${FUNCTION_NAME})
  --webhook-url <url>       bank-server webhook URL for the Lambda and bank-agent (they share this one
                             Service/port); auto-detected from the bank-server-webhook Service if omitted
  --credex-discovery-url <url>
                             Cofide Credex OIDC discovery endpoint for bank-agent's On-Behalf-Of token
                             exchange Credential Provider — must be the full discovery document URL
                             (ending in /.well-known/openid-configuration), since AWS's AgentCore
                             Credential Provider requires exactly that suffixed form. Defaults to
                             --credex-url with that suffix appended if omitted — pass this explicitly
                             if Credex exposes a different endpoint for this than for bank-lambda's
                             bespoke exchange, see workloads/bank/docs/agentcore-identity.md
  --image-prefix <prefix>   Image prefix (default: ${IMAGE_PREFIX}) — must match whatever
                             deploy-static.sh/just build-bank used, so a rebuilt image is actually
                             found under this reference
  --image-tag <tag>         Image tag (default: ${IMAGE_TAG})
  --kind-cluster <name>     Load freshly-built images into this kind cluster before restarting;
                             auto-detected from the current kubectl context (kind-<name>) if omitted.
                             Only applies when --image-prefix is ko.local/ — without this, a rebuilt
                             image only reaches the local Docker daemon, never the kind node's
                             containerd, so the restarted pod silently keeps running the old code
  --skip-helm               Skip the Helm upgrade + rollout restart step
  --skip-terraform          Skip the Terraform apply step
  --skip-kind-load          Don't load images into a kind cluster, even if one is detected
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
    --bootstrap-dir) BOOTSTRAP_DIR="$2"; shift 2 ;;
    --server-spiffe-id) SERVER_SPIFFE_ID="$2"; shift 2 ;;
    --client-spiffe-id) CLIENT_SPIFFE_ID="$2"; shift 2 ;;
    --lambda-spiffe-id) LAMBDA_SPIFFE_ID="$2"; shift 2 ;;
    --fraud-checker-spiffe-id) FRAUD_CHECKER_SPIFFE_ID="$2"; shift 2 ;;
    --credex-url) CREDEX_URL="$2"; shift 2 ;;
    --credex-audience) CREDEX_AUDIENCE="$2"; shift 2 ;;
    --credex-discovery-url) CREDEX_DISCOVERY_URL="$2"; shift 2 ;;
    --aws-region) AWS_REGION="$2"; shift 2 ;;
    --function-name) FUNCTION_NAME="$2"; shift 2 ;;
    --webhook-url) WEBHOOK_URL="$2"; shift 2 ;;
    --image-prefix) IMAGE_PREFIX="$2"; shift 2 ;;
    --image-tag) IMAGE_TAG="$2"; shift 2 ;;
    --kind-cluster) KIND_CLUSTER="$2"; shift 2 ;;
    --skip-helm) SKIP_HELM=1; shift ;;
    --skip-terraform) SKIP_TERRAFORM=1; shift ;;
    --skip-kind-load) SKIP_KIND_LOAD=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

require kube-context "$KUBE_CONTEXT"

namespace_args=()
[[ -n "$NAMESPACE" ]] && namespace_args=(--namespace "$NAMESPACE")

# credex_discovery_url must be the full discovery document URL (ending in
# /.well-known/openid-configuration) — AWS's AgentCore Credential Provider
# requires exactly this suffixed form (enforced server-side by a regex), and
# bank-server's own code expects the same shape (it no longer appends the
# suffix itself). If --credex-discovery-url wasn't given, derive it from
# --credex-url by appending the standard suffix, rather than passing the
# bare issuer through unsuffixed.
RESOLVED_CREDEX_DISCOVERY_URL="${CREDEX_DISCOVERY_URL:-${CREDEX_URL%/}/.well-known/openid-configuration}"

if [[ "$SKIP_HELM" -eq 0 ]]; then
  require server-spiffe-id "$SERVER_SPIFFE_ID"
  require client-spiffe-id "$CLIENT_SPIFFE_ID"
  require lambda-spiffe-id "$LAMBDA_SPIFFE_ID"
  require fraud-checker-spiffe-id "$FRAUD_CHECKER_SPIFFE_ID"

  echo "==> Detecting bank-agent's authorized actor (IAM execution role ARN) from terraform/ output"
  if ! AGENT_AUTHORIZED_ACTOR="$(terraform -chdir="$TERRAFORM_DIR" output -raw bank_agent_execution_role_arn 2>/dev/null)" || [[ -z "$AGENT_AUTHORIZED_ACTOR" ]]; then
    echo "Error: could not read bank_agent_execution_role_arn from ${TERRAFORM_DIR}. Run 'terraform apply' there first (see README)." >&2
    exit 1
  fi
  echo "    ${AGENT_AUTHORIZED_ACTOR}"

  # Without this, a rebuilt ko.local/ image never reaches the kind node's
  # containerd — imagePullPolicy=Never means the rollout restart below just
  # recreates the pod against whatever's already cached there, silently
  # keeping the old code running even after a real rebuild.
  maybe_kind_load "$IMAGE_PREFIX" "$KIND_CLUSTER" "$SKIP_KIND_LOAD" \
    "${IMAGE_PREFIX}bank-server:${IMAGE_TAG}" "${IMAGE_PREFIX}bank-client:${IMAGE_TAG}"

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
    --set spiffe.lambdaSpiffeId="$LAMBDA_SPIFFE_ID" \
    --set spiffe.fraudCheckerSpiffeId="$FRAUD_CHECKER_SPIFFE_ID" \
    --set spiffe.agentAuthorizedActor="$AGENT_AUTHORIZED_ACTOR" \
    --set credex.discoveryUrl="$RESOLVED_CREDEX_DISCOVERY_URL"

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

  echo "==> Detecting OIDC client from terraform/bootstrap output"
  if ! OIDC_CLIENT_ID="$(terraform -chdir="$BOOTSTRAP_DIR" output -raw oidc_client_id 2>/dev/null)" || [[ -z "$OIDC_CLIENT_ID" ]]; then
    echo "Error: could not read oidc_client_id from ${BOOTSTRAP_DIR}. Run 'terraform apply' there first (see README)." >&2
    exit 1
  fi
  if ! OIDC_DISCOVERY_URL="$(terraform -chdir="$BOOTSTRAP_DIR" output -raw oidc_discovery_url 2>/dev/null)" || [[ -z "$OIDC_DISCOVERY_URL" ]]; then
    echo "Error: could not read oidc_discovery_url from ${BOOTSTRAP_DIR}. Run 'terraform apply' there first (see README)." >&2
    exit 1
  fi
  echo "    client_id=${OIDC_CLIENT_ID}"
  echo "    discovery_url=${OIDC_DISCOVERY_URL}"

  if [[ -z "$WEBHOOK_URL" ]]; then
    echo "==> Detecting bank-server-webhook URL"
    if ! WEBHOOK_URL="$(detect_webhook_url)"; then
      echo "Error: could not auto-detect the webhook URL. Pass --webhook-url explicitly." >&2
      exit 1
    fi
    echo "    ${WEBHOOK_URL}"
  fi

  AGENT_API_URL="$(agent_api_url_from_webhook_url "$WEBHOOK_URL")"

  echo "==> terraform apply (auth_mode=spiffe)"
  terraform -chdir="$TERRAFORM_DIR" init -input=false
  terraform -chdir="$TERRAFORM_DIR" apply \
    -var "aws_region=${AWS_REGION}" \
    -var "function_name=${FUNCTION_NAME}" \
    -var "auth_mode=spiffe" \
    -var "bank_server_webhook_url=${WEBHOOK_URL}" \
    -var "token_exchange_url=${CREDEX_URL}" \
    -var "credex_audience=${CREDEX_AUDIENCE}" \
    -var "bank_server_agent_api_url=${AGENT_API_URL}" \
    -var "oidc_discovery_url=${OIDC_DISCOVERY_URL}" \
    -var "oidc_allowed_clients=[\"${OIDC_CLIENT_ID}\"]" \
    -var "credex_discovery_url=${RESOLVED_CREDEX_DISCOVERY_URL}"
else
  echo "==> Skipping Terraform (--skip-terraform)"
fi

cat <<EOF

Done. Invoke the Lambda again and reload the dashboard — the header badge
should now read "Connected via SPIFFE":
  aws lambda invoke --function-name ${FUNCTION_NAME} \\
    --payload '{"merchant": "Rail Delivery Group", "category": "Transport", "amountPence": -3450}' \\
    --cli-binary-format raw-in-base64-out out.json

Note: this only works if ${SERVER_SPIFFE_ID:-<server-spiffe-id>}, ${CLIENT_SPIFFE_ID:-<client-spiffe-id>},
${LAMBDA_SPIFFE_ID:-<lambda-spiffe-id>}, and ${FRAUD_CHECKER_SPIFFE_ID:-<fraud-checker-spiffe-id>} are
already registered in your trust zone/Credex config — that registration happens outside this repo.
EOF

#!/usr/bin/env bash
# Build and push bank-agent's container image to ECR. Unlike bank-server and
# bank-client (built with `ko` via `just build-bank`), bank-agent isn't
# ko-buildable — it targets AWS Bedrock AgentCore Runtime, which requires an
# ARM64 image in ECR rather than a `ko.local`/generic-registry image. Run
# `terraform apply` in terraform/bootstrap first (see README), so the ECR
# repository this pushes to already exists.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

AGENT_DIR="$(cd "${SCRIPT_DIR}/../bank-agent" && pwd)"
TERRAFORM_DIR="$(resolve_terraform_dir)"
BOOTSTRAP_DIR="$(resolve_bootstrap_dir)"
ECR_REPOSITORY_URL=""
TAG=""

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Options:
  --ecr-repository-url <url>  ECR repository URL to push to; auto-detected from
                               'terraform output repository_url' in --bootstrap-dir if omitted
  --tag <tag>                 Image tag (default: current UTC timestamp, YYYYMMDDHHMMSS). Leave this
                               as the default unless you have a reason to override it —
                               terraform/agentcore.tf picks up whichever pushed tag sorts highest as a
                               14-digit string, so a fresh timestamp on every build is what makes each
                               'terraform apply' automatically pick up the image just pushed here, with
                               nothing to copy between the two steps.
  --bootstrap-dir <path>      terraform/bootstrap module path, used for auto-detection
                               (default: ${BOOTSTRAP_DIR})
  --terraform-dir <path>      Main Terraform config path, only used in the completion hint
                               (default: ${TERRAFORM_DIR})
  -h, --help                  Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ecr-repository-url) ECR_REPOSITORY_URL="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --bootstrap-dir) BOOTSTRAP_DIR="$2"; shift 2 ;;
    --terraform-dir) TERRAFORM_DIR="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

if [[ -z "$TAG" ]]; then
  TAG="$(date -u +%Y%m%d%H%M%S)"
fi

if [[ -z "$ECR_REPOSITORY_URL" ]]; then
  echo "==> Detecting ECR repository URL from terraform/bootstrap output"
  if ! ECR_REPOSITORY_URL="$(terraform -chdir="$BOOTSTRAP_DIR" output -raw repository_url 2>/dev/null)" || [[ -z "$ECR_REPOSITORY_URL" ]]; then
    echo "Error: could not auto-detect the ECR repository URL. Run 'terraform apply' in ${BOOTSTRAP_DIR} first" \
         "(see README), or pass --ecr-repository-url explicitly." >&2
    exit 1
  fi
  echo "    ${ECR_REPOSITORY_URL}"
fi

REGISTRY="${ECR_REPOSITORY_URL%%/*}"
AWS_REGION="$(cut -d. -f4 <<<"$REGISTRY")"

echo "==> Logging in to ${REGISTRY}"
aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$REGISTRY"

echo "==> Building ${ECR_REPOSITORY_URL}:${TAG} (linux/arm64 — AgentCore Runtime requires ARM64)"
# --provenance=false --sbom=false: recent buildx versions attach extra
# attestation manifests by default, which just adds more that has to survive
# the push to ECR — skip them, AgentCore Runtime doesn't consume them anyway.
docker buildx build --platform linux/arm64 --provenance=false --sbom=false \
  -t "${ECR_REPOSITORY_URL}:${TAG}" --push "$AGENT_DIR"

cat <<EOF

Done. Deploy it with:
  cd ${TERRAFORM_DIR}
  terraform apply ...  # plus your usual auth_mode/OIDC/Credex vars — no need to reference tag
                        # ${TAG}, terraform/agentcore.tf auto-detects the most recently pushed image
EOF

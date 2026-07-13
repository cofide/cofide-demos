#!/usr/bin/env bash
# Build and push bank-fraud-checker's container image to ECR. Unlike every
# other workload in this demo, bank-fraud-checker's infrastructure (the ECR
# repository, and the EC2 instance it actually runs on) lives in the
# cofide-connect repo, not here — it's deployed as a third Docker container
# on the same VM that already runs a SPIRE agent + cofide-node-observer for
# node-observer demos (see cofide-connect's
# cloud-discovery-poc/cofide-demo-infra/node-observer-instance Terraform
# module). This script only builds and pushes the image; applying that
# Terraform (and the sibling bank-fraud-checker-ecr module, first) is a
# separate, manual step in the other repo.
#
# Must be run with the repo root as the Docker build context (see
# ../bank-fraud-checker/Dockerfile's comment) — this script does that for you.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
DOCKERFILE="${SCRIPT_DIR}/../bank-fraud-checker/Dockerfile"

ECR_REPOSITORY_URL=""
TAG=""
CONNECT_REPO_DIR="$(cd "${REPO_ROOT}/../cofide-connect" 2>/dev/null && pwd || true)"
CONNECT_ECR_MODULE="cloud-discovery-poc/cofide-demo-infra/bank-fraud-checker-ecr"

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Options:
  --ecr-repository-url <url>  ECR repository URL to push to; auto-detected from
                               'terraform output repository_url' in
                               <connect-repo-dir>/${CONNECT_ECR_MODULE} if omitted
  --connect-repo-dir <path>   Path to a checkout of cofide-connect, used for
                               auto-detection (default: ${CONNECT_REPO_DIR:-<not found — pass this explicitly>})
  --tag <tag>                 Image tag (default: current UTC timestamp, YYYYMMDDHHMMSS).
                               Leave this as the default unless you have a reason to
                               override it — the ECR repository is tag-immutable, and
                               node-observer-instance's Terraform is expected to be told
                               the exact tag to run via its bank_fraud_checker_image_tag
                               variable (there's no auto-detection on that side, unlike
                               bank-agent's ECR image lookup).
  -h, --help                  Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ecr-repository-url) ECR_REPOSITORY_URL="$2"; shift 2 ;;
    --connect-repo-dir) CONNECT_REPO_DIR="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

if [[ -z "$TAG" ]]; then
  TAG="$(date -u +%Y%m%d%H%M%S)"
fi

if [[ -z "$ECR_REPOSITORY_URL" ]]; then
  if [[ -z "$CONNECT_REPO_DIR" ]]; then
    echo "Error: could not find a cofide-connect checkout next to this repo, and no" \
         "--ecr-repository-url was given. Pass --connect-repo-dir or --ecr-repository-url explicitly." >&2
    exit 1
  fi
  echo "==> Detecting ECR repository URL from ${CONNECT_REPO_DIR}/${CONNECT_ECR_MODULE} output"
  if ! ECR_REPOSITORY_URL="$(terraform -chdir="${CONNECT_REPO_DIR}/${CONNECT_ECR_MODULE}" output -raw repository_url 2>/dev/null)" || [[ -z "$ECR_REPOSITORY_URL" ]]; then
    echo "Error: could not auto-detect the ECR repository URL. Run 'terraform apply' in" \
         "${CONNECT_REPO_DIR}/${CONNECT_ECR_MODULE} first, or pass --ecr-repository-url explicitly." >&2
    exit 1
  fi
  echo "    ${ECR_REPOSITORY_URL}"
fi

REGISTRY="${ECR_REPOSITORY_URL%%/*}"
AWS_REGION="$(cut -d. -f4 <<<"$REGISTRY")"

echo "==> Logging in to ${REGISTRY}"
aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$REGISTRY"

echo "==> Building ${ECR_REPOSITORY_URL}:${TAG}"
# --provenance=false --sbom=false: skip the extra attestation manifests recent
# buildx versions attach by default — nothing on the consuming side (a plain
# `docker run` in a systemd unit) reads them.
docker buildx build --provenance=false --sbom=false \
  -f "$DOCKERFILE" -t "${ECR_REPOSITORY_URL}:${TAG}" --push "$REPO_ROOT"

cat <<EOF

Done. Pass this tag to node-observer-instance's Terraform:
  bank_fraud_checker_image_tag = "${TAG}"
EOF

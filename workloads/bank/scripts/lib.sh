#!/usr/bin/env bash
# Shared helpers for the bank demo deploy scripts. Sourced, not executed.

# resolve_chart_dir echoes the path to the Helm chart, relative to this
# script's own location so the deploy scripts work from any cwd.
resolve_chart_dir() {
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  echo "${script_dir}/../chart/bank"
}

# resolve_terraform_dir echoes the path to the Terraform config.
resolve_terraform_dir() {
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  echo "${script_dir}/../terraform"
}

# detect_webhook_url tries to work out a URL for bank-server-webhook that's
# reachable from AWS: a LoadBalancer hostname/IP if one has been assigned,
# falling back to the ClusterIP (which will only actually be reachable from
# AWS if you've wired up some other path to it, e.g. a VPN or Ingress).
detect_webhook_url() {
  local kubectl_args=()
  [[ -n "${NAMESPACE:-}" ]] && kubectl_args+=(-n "$NAMESPACE")
  [[ -n "${KUBE_CONTEXT:-}" ]] && kubectl_args+=(--context "$KUBE_CONTEXT")

  local port
  port=$(kubectl get svc bank-server-webhook "${kubectl_args[@]}" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null) || return 1
  [[ -n "$port" ]] || return 1

  local host
  host=$(kubectl get svc bank-server-webhook "${kubectl_args[@]}" -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null)
  if [[ -z "$host" ]]; then
    host=$(kubectl get svc bank-server-webhook "${kubectl_args[@]}" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)
  fi
  if [[ -z "$host" ]]; then
    host=$(kubectl get svc bank-server-webhook "${kubectl_args[@]}" -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
  fi
  [[ -n "$host" ]] || return 1

  echo "http://${host}:${port}/webhook/transactions"
}

require() {
  local name="$1" value="$2"
  if [[ -z "$value" ]]; then
    echo "Error: --${name} is required" >&2
    exit 1
  fi
}

# maybe_kind_load loads built images into a kind cluster's nodes. `ko build`
# with KO_DOCKER_REPO=ko.local only loads images into the host Docker daemon
# — kind nodes run their own containerd and can't see them otherwise,
# resulting in ErrImageNeverPull. No-op unless images are prefixed
# "ko.local/" and either a cluster name was given explicitly or the target
# kubectl context (the global KUBE_CONTEXT if set, otherwise the ambient
# current-context) looks like a kind cluster (kind-<name>).
maybe_kind_load() {
  local image_prefix="$1" cluster_override="$2" skip="$3"
  shift 3
  local images=("$@")

  if [[ "$skip" -eq 1 || "$image_prefix" != "ko.local/" ]]; then
    return 0
  fi

  local cluster="$cluster_override"
  if [[ -z "$cluster" ]]; then
    local ctx="${KUBE_CONTEXT:-}"
    if [[ -z "$ctx" ]]; then
      ctx="$(kubectl config current-context 2>/dev/null || true)"
    fi
    [[ "$ctx" == kind-* ]] || return 0
    cluster="${ctx#kind-}"
  fi

  if ! command -v kind &>/dev/null; then
    echo "Error: current context looks like a kind cluster (${cluster}) and images are under ko.local/," \
         "but 'kind' is not installed to load them. Install kind, or pass --skip-kind-load if you've" \
         "already loaded the images yourself." >&2
    exit 1
  fi

  echo "==> Loading images into kind cluster '${cluster}'"
  kind load docker-image "${images[@]}" --name "$cluster"
}
